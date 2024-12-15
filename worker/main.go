package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/nsqio/go-nsq"
)

const NSQ_LOOKUPD_SERVER = "nsqlookupd:4161"
const NSQ_PRIORITY_TOPIC string = "factorization_priority_topic"
const NSQLOOKUPD_STATS_URL = "http://nsqlookupd:4161/nodes"

var NUMBER_OF_WORKERS = runtime.NumCPU() * 2

// there is only meant to be one of these coordinators for now, mostly because of redis
type Worker struct {
	Consumer *nsq.Consumer
	Rdb      *redis.Client
}

func (wc *Worker) GetQueueSize() int64 {
	resp, err := http.Get(NSQ_PRIORITY_TOPIC)
	if err != nil {
		log.Printf("Error getting NSQ stats: %v", err)
		return 0
	}
	defer resp.Body.Close()

	var nsqStats struct {
		Topics []struct {
			TopicName string `json:"topic_name"`
			Depth     int64  `json:"depth"`
		} `json:"topics"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&nsqStats); err != nil {
		log.Printf("Error decoding NSQ stats: %v", err)
		return 0
	}

	for _, topic := range nsqStats.Topics {
		if topic.TopicName == NSQ_PRIORITY_TOPIC {
			log.Printf("Current queue size for topic %s: %d", NSQ_PRIORITY_TOPIC, topic.Depth)
			return topic.Depth
		} else {
			log.Printf("Topic %s not found", topic.TopicName)
			return 0
		}
	}

	return 0
}

func primeFactorization(number string) []string {
	// Create a temporary Python script
	script := fmt.Sprintf(`
import sympy
number = %s
factors = sympy.factorint(%s)
# Convert to format matching current output
result = []
for prime, exp in factors.items():
    result.extend([str(prime)] * exp)
print(','.join(map(str, result)))
`, number, number)

	// Write script to temporary file
	tmpfile, err := os.CreateTemp("", "factor*.py")
	if err != nil {
		log.Printf("Error creating temp file: %v", err)
		return []string{number}
	}
	defer os.Remove(tmpfile.Name())

	if _, err := tmpfile.Write([]byte(script)); err != nil {
		log.Printf("Error writing to temp file: %v", err)
		return []string{number}
	}
	tmpfile.Close()

	// Execute Python script
	cmd := exec.Command("python3", tmpfile.Name())
	output, err := cmd.Output()
	if err != nil {
		log.Printf("Error running Python script: %v", err)
		return []string{number}
	}

	// Parse output
	factors := strings.Split(strings.TrimSpace(string(output)), ",")
	return factors
}

/*
Potential Optimization: When dividing by a factor,
you're doing the division in-place with n.Div(n, i). This is fine, but you might want to store the result in a temporary variable first to ensure
thread safety if this function is called concurrently.

	However, since you're creating new numbers for each call, this isn't strictly necessary.

Otherwise, the logic looks correct:
It properly handles numbers less than 2
It correctly factors out 2's first (optimization)
It checks odd numbers starting from 3
It correctly handles the case where the remaining number is prime
It uses GMP's arbitrary precision arithmetic correctly
The comparisons with zero and two are done correctly
The string conversions are handled properly
The algorithm is a basic trial division implementation
which is suitable for most numbers. For very large numbers
(hundreds of digits), you might want to consider implementing more

	sophisticated algorithms like Pollard's rho or the quadratic sieve,
	but for general use, this implementation is fine.
*/
// func DEPRICATEDprimeFactorization(number string) []string {
// 	n := new(gmp.Int)
// 	n.SetString(number, 10)

// 	if n.Cmp(gmp.NewInt(2)) < 0 {
// 		return []string{number}
// 	}

// 	var factors []string
// 	two := gmp.NewInt(2)
// 	zero := gmp.NewInt(0)

// 	// Handle 2 as a special case
// 	for n.Mod(n, two).Cmp(zero) == 0 {
// 		factors = append(factors, "2")
// 		n.Div(n, two)
// 	}

// 	// Create a GMP integer for the incrementing factor
// 	i := gmp.NewInt(3)

// 	// Create temporary variables for calculations
// 	temp := new(gmp.Int)
// 	// sqrt := new(gmp.Int)

// 	// While i*i <= n
// 	for {
// 		temp.Mul(i, i)
// 		if temp.Cmp(n) > 0 {
// 			break
// 		}

// 		// While n is divisible by i
// 		for n.Mod(n, i).Cmp(zero) == 0 {
// 			factors = append(factors, i.String())
// 			n.Div(n, i)
// 		}

// 		i.Add(i, two)
// 	}

// 	// If n > 2, n is prime
// 	if n.Cmp(two) > 0 {
// 		factors = append(factors, n.String())
// 	}

// 	return factors
// }

func NewWorker() (*Worker, error) {
	config := nsq.NewConfig()
	config.MaxInFlight = 5 // Limit concurrent messages
	config.MaxRequeueDelay = time.Second * 90
	config.DefaultRequeueDelay = time.Second * 30

	consumer, err := nsq.NewConsumer(NSQ_PRIORITY_TOPIC, "channel", config)
	if err != nil {
		return nil, fmt.Errorf("could not create NSQ consumer: %w", err)
	}

	rdb := redis.NewClient(&redis.Options{
		Addr: os.Getenv("REDIS_URL"),
	})

	// After creating the consumer
	log.Printf("Consumer created for topic: %s", NSQ_PRIORITY_TOPIC)
	consumer.AddHandler(nsq.HandlerFunc(func(message *nsq.Message) error {
		message.Touch()
		// Parse message into struct
		requestID := string(message.Body)
		if requestID == "init" {
			message.Finish()
			log.Printf("Initializing worker")
			return nil
		}

		// Get all fields from Redis hash
		ctx := context.Background()
		values, err := rdb.HGetAll(ctx, "request:"+requestID).Result()
		if err != nil {
			log.Printf("Failed to get request data from Redis: %v", err)
			message.Requeue(-1)
			return nil
		}

		// Parse the values into the struct
		callerID := values["caller_id"]
		// status := values["status"]
		number := values["number"]
		count := values["processed_at_count"]

		primeFactors := primeFactorization(number)

		log.Printf("Processing request - CallerID: %s, Number: %s", callerID, number)

		// Store request data, this will be the state used by client, and state for storing results
		// Convert slice of strings to JSON string for storage
		resultsJSON, err := json.Marshal(primeFactors)
		if err != nil {
			log.Printf("Failed to marshal results: %v", err)
			message.Requeue(-1)
			return nil
		}

		err = rdb.HSet(ctx, "request:"+requestID,
			"caller_id", callerID,
			"status", "completed",
			"number", number,
			"processed_at_count", count,
			"results", string(resultsJSON),
		).Err()
		if err != nil {
			log.Printf("Failed to store request data in Redis: %v", err)
			message.Requeue(-1) // Requeue message with default timeout
			return nil
		}
		// Mark message as finished in NSQ
		message.Finish()
		return nil
	}))

	// Connect to NSQLookupd AFTER adding the handler
	err = consumer.ConnectToNSQLookupd(NSQ_LOOKUPD_SERVER)
	if err != nil {
		log.Fatal("Could not connect to NSQ lookup:", err)
	}
	log.Printf("Successfully connected to NSQLookupd")

	// You might also want to add a stats handler to monitor the consumer
	consumer.SetLogger(log.New(os.Stdout, "", log.LstdFlags), nsq.LogLevelDebug)

	return &Worker{
		Consumer: consumer,
		Rdb:      rdb,
	}, nil

}

func main() {
	// Limit number of workers
	maxWorkers := runtime.NumCPU() * 2 // reduced from 5 to 2

	instances := make([]*Worker, maxWorkers)
	for i := 0; i < maxWorkers; i++ {
		instance, err := NewWorker()
		if err != nil {
			log.Printf("Failed to create worker: %v", err)
			continue
		}
		instances[i] = instance
	}

	// Add graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Println("Shutting down workers...")
		for _, worker := range instances {
			if worker != nil && worker.Consumer != nil {
				worker.Consumer.Stop()
			}
		}
		os.Exit(0)
	}()

	select {}
}
