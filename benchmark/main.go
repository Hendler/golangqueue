package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/big"
	"math/rand"
	"net/http"
	"sync"
	"time"
)

type Request struct {
	Number string `json:"number"`
}

type Response struct {
	RequestID string `json:"request_id"`
}

type BenchmarkStats struct {
	TotalRequests   int
	SuccessfulPOSTs int
	FailedPOSTs     int
	AveragePOSTTime time.Duration
	MaxPOSTTime     time.Duration
	MinPOSTTime     time.Duration
	mutex           sync.Mutex
	postTimes       []time.Duration
	PendingRequests []string
	pendingMutex    sync.Mutex
}

func (stats *BenchmarkStats) addPostTime(duration time.Duration) {
	stats.mutex.Lock()
	defer stats.mutex.Unlock()

	stats.postTimes = append(stats.postTimes, duration)
	stats.AveragePOSTTime = calculateAverage(stats.postTimes)

	if duration > stats.MaxPOSTTime {
		stats.MaxPOSTTime = duration
	}
	if stats.MinPOSTTime == 0 || duration < stats.MinPOSTTime {
		stats.MinPOSTTime = duration
	}
}

func calculateAverage(durations []time.Duration) time.Duration {
	if len(durations) == 0 {
		return 0
	}
	var sum time.Duration
	for _, d := range durations {
		sum += d
	}
	return sum / time.Duration(len(durations))
}

func generateLargeNumber() string {
	// Generate a random number between 10^6 and 10^20
	min := new(big.Int).Exp(big.NewInt(10), big.NewInt(6), nil)
	max := new(big.Int).Exp(big.NewInt(10), big.NewInt(20), nil)

	diff := new(big.Int).Sub(max, min)
	random := new(big.Int).Rand(rand.New(rand.NewSource(time.Now().UnixNano())), diff)
	random.Add(random, min)

	return random.String()
}

func generateClientID() string {
	return fmt.Sprintf("client-%d", rand.Intn(1000000))
}

func sendRequest(url string, clientID string, stats *BenchmarkStats, wg *sync.WaitGroup) {
	defer wg.Done()

	number := generateLargeNumber()
	reqBody := Request{Number: number}
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		fmt.Printf("Error marshaling request: %v\n", err)
		stats.mutex.Lock()
		stats.FailedPOSTs++
		stats.mutex.Unlock()
		return
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		fmt.Printf("Error creating request: %v\n", err)
		stats.mutex.Lock()
		stats.FailedPOSTs++
		stats.mutex.Unlock()
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Caller-ID", clientID)

	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	duration := time.Since(start)

	if err != nil {
		fmt.Printf("Error sending request: %v\n", err)
		stats.mutex.Lock()
		stats.FailedPOSTs++
		stats.mutex.Unlock()
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("Non-200 response: %d - %s\n", resp.StatusCode, string(body))
		stats.mutex.Lock()
		stats.FailedPOSTs++
		stats.mutex.Unlock()
		return
	}

	var response Response
	if err := json.Unmarshal(body, &response); err != nil {
		fmt.Printf("Error parsing response: %v\n", err)
		stats.mutex.Lock()
		stats.FailedPOSTs++
		stats.mutex.Unlock()
		return
	}

	stats.mutex.Lock()
	stats.SuccessfulPOSTs++
	stats.mutex.Unlock()
	stats.addPostTime(duration)
	stats.pendingMutex.Lock()
	stats.PendingRequests = append(stats.PendingRequests, response.RequestID)
	stats.pendingMutex.Unlock()
}

func checkAllResults(baseURL string, stats *BenchmarkStats) {
	for {
		all_completed := true
		stats.pendingMutex.Lock()
		pendingRequests := make([]string, len(stats.PendingRequests))
		copy(pendingRequests, stats.PendingRequests)
		stats.pendingMutex.Unlock()

		for _, requestID := range pendingRequests {
			url := fmt.Sprintf("%s/%s", baseURL, requestID)
			resp, err := http.Get(url)
			if err != nil {
				fmt.Printf("Error checking result for request %s: %v\n", requestID, err)
				all_completed = false
				continue
			}
			defer resp.Body.Close()

			var result map[string]interface{}
			body, _ := io.ReadAll(resp.Body)
			if err := json.Unmarshal(body, &result); err != nil {
				fmt.Printf("Error parsing response for request %s: %v\n", requestID, err)
				all_completed = false
				continue
			}

			status, exists := result["status"]
			if !exists {
				if errMsg, hasError := result["error"]; hasError {
					fmt.Printf("Error for request %s: %v\n", requestID, errMsg)
					all_completed = false
				}
				continue
			}

			if status == "completed" {
				fmt.Printf("Completed request %s: %s\n", requestID, string(body))
			} else {
				fmt.Printf("Pending request %s: status = %v\n", requestID, status)
				all_completed = false
			}
		}

		if all_completed {
			fmt.Println("All requests have completed!")
			break
		}

		time.Sleep(2 * time.Second)
	}
}

func main() {
	// Command line flags:
	// -requests=N    Total number of requests to distribute across clients (default: 20000)
	//                Each client will receive roughly equal share of requests
	// -clients=N     Number of unique client IDs to simulate (default: 10)
	//                Each client gets a random UUID as identifier
	// -concurrency=N Maximum number of concurrent requests (default: 50)
	//                Controls how many requests can be in-flight at once
	// -url=URL      Target URL for sending requests (default: http://localhost:5555/compute)
	//               Must be a valid HTTP/HTTPS URL
	totalRequests := flag.Int("requests", 20000, "Total number of requests to send")
	totalClients := flag.Int("clients", 10, "Number of unique clients to simulate")
	concurrency := flag.Int("concurrency", 50, "Number of concurrent requests")
	url := flag.String("url", "http://localhost:5555/compute", "Target URL")
	flag.Parse()

	fmt.Printf("Starting benchmark with:\n- Total Requests: %d\n- Total Clients: %d\n- Concurrency: %d\n- URL: %s\n\n",
		*totalRequests, *totalClients, *concurrency, *url)

	// Generate client IDs
	clientIDs := make([]string, *totalClients)
	for i := range clientIDs {
		clientIDs[i] = generateClientID()
	}

	stats := &BenchmarkStats{
		TotalRequests: *totalRequests,
	}

	// Create a semaphore channel to limit concurrency
	sem := make(chan bool, *concurrency)
	var wg sync.WaitGroup

	startTime := time.Now()

	// Distribute requests evenly among clients
	requestsPerClient := *totalRequests / *totalClients
	remainingRequests := *totalRequests % *totalClients

	for i, clientID := range clientIDs {
		requests := requestsPerClient
		if i < remainingRequests {
			requests++
		}

		for j := 0; j < requests; j++ {
			wg.Add(1)
			sem <- true // Acquire semaphore
			go func(cID string) {
				sendRequest(*url, cID, stats, &wg)
				<-sem // Release semaphore
			}(clientID)
		}
	}

	wg.Wait()
	duration := time.Since(startTime)

	// Print results
	fmt.Printf("\nBenchmark Results:\n")
	fmt.Printf("Total Duration: %v\n", duration)
	fmt.Printf("Successful Requests: %d\n", stats.SuccessfulPOSTs)
	fmt.Printf("Failed Requests: %d\n", stats.FailedPOSTs)
	fmt.Printf("Average Request Time: %v\n", stats.AveragePOSTTime)
	fmt.Printf("Maximum Request Time: %v\n", stats.MaxPOSTTime)
	fmt.Printf("Minimum Request Time: %v\n", stats.MinPOSTTime)
	fmt.Printf("Requests/second: %.2f\n", float64(stats.SuccessfulPOSTs)/duration.Seconds())

	// Check computation results
	fmt.Printf("\nChecking computation results...\n")
	checkAllResults(*url, stats)
}
