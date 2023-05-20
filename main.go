package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/tealeg/xlsx"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type ClusterDetails struct {
	Count       int
	ClusterInfo []ClusterIn
}

type ClusterIn struct {
	Status     string `bson:"clusterInfo.status.status"`
	CreateTime string `bson:"metadata.createTime"`
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
	pastclustercount := make(map[string]ClusterDetails)
	// Iterate over each namespace.
	for _, namespace := range namespaceList.Items {
		namespaceName := namespace.Name
		// Get the MongoDB auth details for the namespace.
		password, err := getSecretValue(clientset, namespaceName, "pxc-backup-mongodb", "mongodb-password")
		if err != nil {
			log.Printf("Failed to get MongoDB password for namespace %s: %v", namespaceName, err)
			continue
		}
		username, err := getSecretValue(clientset, namespaceName, "pxc-backup-mongodb", "mongodb-username")
		if err != nil {
			log.Printf("Failed to get MongoDB username for namespace %s: %v", namespaceName, err)
			continue
		}
		// fmt.Printf("Username::%s\nPassword::%s\n", username, password)

		// Connect to the MongoDB pod in the namespace.
		srcClient, err := connectMongoDB("pxc-backup-mongodb-headless", namespaceName, password, username)
		if err != nil {
			log.Printf("Failed to connect to MongoDB in namespace %s: %v", namespaceName, err)
			continue
		}
		collectionName := "clusterobjects"
		clusterDetails, err := attachedClusterDetails(srcClient, true, "px-backup", collectionName)
		if err != nil {
			log.Printf("Error getting details of collection %s in namespace %s: %v", collectionName, namespaceName, err)
			continue
		}
		if clusterDetails.Count > 0 {
			clustercount[namespaceName] = clusterDetails
		}
		pastClusterDetails, err := attachedClusterDetails(srcClient, false, "px-backup", collectionName)
		if err != nil {
			log.Printf("Error getting details of collection %s in namespace %s: %v", collectionName, namespaceName, err)
			continue
		}
		if pastClusterDetails.Count > 0 {
			pastclustercount[namespaceName] = pastClusterDetails

		}
	}
	fmt.Printf("Total %d customers have added clusters after Private IP release\n", len(clustercount))
	fmt.Printf("Total %d customers have added clusters before Private IP release\n", len(pastclustercount))
	err = writeMapToCSV("cluster.xlsx", "pvtIP", clustercount)
	if err != nil {
		log.Printf("Writing to CSV failed due to::%s!\n", err)
	}
	err = writeMapToCSV("cluster.xlsx", "Non-pvtIP", pastclustercount)
	if err != nil {
		log.Printf("Writing to CSV failed due to::%s!\n", err)
	}
	time.Sleep(10000)
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

func attachedClusterDetails(mongoClient *mongo.Client, boolVal bool, dbName, collectionName string) (ClusterDetails, error) {
	// Access the specified database and collection
	collection := mongoClient.Database(dbName).Collection(collectionName)

	// Set up the context
	ctx := context.TODO()

	// Filter definition
	filter := bson.M{
		"clusterInfo.teleportClusterId": bson.M{"$exists": boolVal},
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
	var data []ClusterIn
	var value ClusterIn
	for cursor.Next(ctx) {
		var doc map[string]interface{}
		err := cursor.Decode(&doc)
		if err != nil {
			log.Fatal(err)
		}
		metadata := doc["metadata"].(map[string]interface{})
		value.CreateTime = fmt.Sprint(metadata["createTime"])
		clusterInfo := doc["clusterInfo"].(map[string]interface{})["status"].(map[string]interface{})
		value.Status = fmt.Sprint(clusterInfo["status"])
		data = append(data, value)
	}

	// Create and print ClusterDetails struct
	clusterDetails := ClusterDetails{
		Count:       int(count),
		ClusterInfo: data,
	}
	return clusterDetails, nil
}

func writeMapToCSV(filename, sheetName string, data map[string]ClusterDetails) error {
	_, err := os.Stat(filename)
	fileExists := !os.IsNotExist(err)

	var file *xlsx.File
	var sheet *xlsx.Sheet

	if fileExists {
		file, err = xlsx.OpenFile(filename)
		if err != nil {
			return err
		}

		sheet, err = file.AddSheet(sheetName)
		if err != nil {
			return err
		}
	} else {
		file = xlsx.NewFile()
		sheet, err = file.AddSheet(sheetName)
		if err != nil {
			return err
		}

		// Write header
		headerRow := sheet.AddRow()
		headerRow.AddCell().Value = "NameSpace"
		headerRow.AddCell().Value = "ClusterCount"
		headerRow.AddCell().Value = "Status"
		headerRow.AddCell().Value = "CreationTime"
	}

	// Write data
	for ns, cluster := range data {
		for _, details := range cluster.ClusterInfo {
			row := sheet.AddRow()
			row.AddCell().Value = ns
			row.AddCell().Value = fmt.Sprintf("%d", cluster.Count)
			row.AddCell().Value = details.Status
			row.AddCell().Value = details.CreateTime
		}
	}

	err = file.Save(filename)
	if err != nil {
		return err
	}

	return nil
}
