package main

import (
	"context"
	"fmt"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"log"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type ClusterDetails struct {
	Count          int
	ClusterObjects []ClusterObject
}

type ClusterObject struct {
	Name       string
	CreateTime time.Time
}

func main() {
	// Get the Kubernetes client configuration.
	config, err := rest.InClusterConfig()
	if err != nil {
		log.Fatal(err)
	}

	// Create the Kubernetes clientset.
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatal(err)
	}

	// Get all namespaces with the specified label.
	namespaceList, err := clientset.CoreV1().Namespaces().List(context.Background(), v1.ListOptions{LabelSelector: "tenant"})
	if err != nil {
		log.Fatal(err)
	}
	clustercount := make(map[string]ClusterDetails)
	// Iterate over each namespace.
	for _, namespace := range namespaceList.Items {
		namespaceName := namespace.Name
		// Get the MongoDB auth details for the namespace.
		password, err := getSecretValue(clientset, namespaceName, "pxc-backup-mongodb", "mongodb-password")
		username, err := getSecretValue(clientset, namespaceName, "pxc-backup-mongodb", "mongodb-username")
		// fmt.Printf("Username::%s\nPassword::%s\n", username, password)
		if err != nil {
			log.Printf("Failed to get MongoDB credentials for namespace %s: %v", namespaceName, err)
			continue
		}

		// Connect to the MongoDB pod in the namespace.
		srcClient, err := connectMongoDB("pxc-backup-mongodb-headless", namespaceName, password, username)
		if err != nil {
			log.Printf("Failed to connect to MongoDB in namespace %s: %v", namespaceName, err)
			continue
		}
		collectionName := "clusterobjects"
		clusterDetails, err := attachedClusterDetails(srcClient, "px-backup", collectionName)
		if err != nil {
			log.Printf("Error getting details of collection %s in namespace %s: %v", collectionName, namespaceName, err)
			continue
		}
		if clusterDetails.Count > 0 {
			clustercount[namespaceName] = clusterDetails
		}

	}
	log.Printf("Total %d customers have added clusters after Private IP release", len(clustercount))
	for key, value := range clustercount {
		log.Printf("%s has attached %+v cluster details", key, value)
	}
	return
}

func getSecretValue(clientset *kubernetes.Clientset, namespace, secretName, passwordKey string) (string, error) {
	secret, err := clientset.CoreV1().Secrets(namespace).Get(context.Background(), secretName, v1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get secret %s/%s: %v", namespace, secretName, err)
	}

	passwordBytes, ok := secret.Data[passwordKey]
	if !ok {
		return "", fmt.Errorf("password key %s not found in secret %s/%s", passwordKey, namespace, secretName)
	}

	return string(passwordBytes), nil
}

func connectMongoDB(svcname, namespace, password, username string) (*mongo.Client, error) {
	// Set MongoDB connection URI
	uri := fmt.Sprintf("mongodb://%s:%s@%s.%s.svc.cluster.local:27017/?directConnection=true&authSource=px-backup&readPreference=primaryPreferred", username, password, svcname, namespace)

	// Set connection options
	clientOptions := options.Client().ApplyURI(uri)

	// Create a new MongoDB client
	client, err := mongo.Connect(context.TODO(), clientOptions)
	if err != nil {
		return nil, err
	}

	// Ping the MongoDB server to ensure a connection
	err = client.Ping(context.TODO(), nil)
	if err != nil {
		return nil, err
	}

	log.Printf("Connected to MongoDB of namespace::%s!\n", namespace)
	return client, nil
}

func attachedClusterDetails(mongoClient *mongo.Client, dbName, collectionName string) (ClusterDetails, error) {
	// Access the specified database and collection
	collection := mongoClient.Database(dbName).Collection(collectionName)

	// Set up the context
	ctx := context.TODO()

	// Filter definition
	filter := bson.M{
		"clusterInfo.teleportClusterId": bson.M{"$exists": true},
		"clusterInfo.status.status":     "Success",
		"metadata.name":                 bson.M{"$ne": "testdrive-cluster"},
	}

	// Count documents
	count, err := collection.CountDocuments(ctx, filter)
	if err != nil {
		log.Fatal(err)
	}

	// Retrieve documents
	cursor, err := collection.Find(ctx, filter)
	if err != nil {
		log.Fatal(err)
	}
	defer func(cursor *mongo.Cursor, ctx context.Context) {
		err := cursor.Close(ctx)
		if err != nil {
			log.Fatal(err)	
		}
	}(cursor, ctx)

	// Prepare result
	var clusterObjects []ClusterObject
	for cursor.Next(ctx) {
		var obj ClusterObject
		err := cursor.Decode(&obj)
		if err != nil {
			log.Fatal(err)
		}
		clusterObjects = append(clusterObjects, obj)
	}

	// Create and print ClusterDetails struct
	clusterDetails := ClusterDetails{
		Count:          int(count),
		ClusterObjects: clusterObjects,
	}
	return clusterDetails, nil
}
