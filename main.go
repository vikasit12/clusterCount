package main

import (
	"context"
	"fmt"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"log"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

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

	// Iterate over each namespace.
	for _, namespace := range namespaceList.Items {
		namespaceName := namespace.Name
		// Get the MongoDB auth details for the namespace.
		password, err := getSecretValue(clientset, namespaceName, "pxc-backup-mongodb", "mongodb-root-password")
		if err != nil {
			log.Printf("Failed to get MongoDB password for namespace %s: %v", namespaceName, err)
			continue
		}

		// Connect to the MongoDB pod in the namespace.
		srcClient, err := connectMongoDB("pxc-backup-mongodb-headless", namespaceName, password)
		if err != nil {
			log.Printf("Failed to connect to MongoDB in namespace %s: %v", namespaceName, err)
			continue
		}
		collectionName := "clusterobjects"
		count, err := attachedClusterCount(srcClient, "px-backup", collectionName)
		if err != nil {
			log.Printf("Error getting count of collection %s in namespace %s: %v", collectionName, namespaceName, err)
			continue
		}
		if count > 1 {
			log.Printf("Namespace %s has count of %d in collection %s", namespaceName, count, collectionName)
		}

	}
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

func connectMongoDB(svcname, namespace, password string) (*mongo.Client, error) {
	// Set MongoDB connection URI
	uri := fmt.Sprintf("mongodb+srv://%s.%s.svc.cluster.local/?authSource=px-backup&replicaSet=rs0&tls=false", svcname, namespace)
	fmt.Printf("MongoDB URI::%s!\n", uri)

	// Set connection options
	clientOptions := options.Client().ApplyURI(uri)
	clientOptions.SetAuth(options.Credential{
	 	Username: "root",
		Password: password,
	})

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

	fmt.Printf("Connected to MongoDB of namespace::%s!\n", namespace)
	return client, nil
}

func attachedClusterCount(mongoClient *mongo.Client, dbName, collectionName string) (int64, error) {
	// Access the specified database and collection
	collection := mongoClient.Database(dbName).Collection(collectionName)

	// Set up the context
	ctx := context.TODO()

	// Count the documents in the collection
	count, err := collection.CountDocuments(ctx, nil)
	if err != nil {
		return 0, err
	}

	return count, nil
}
