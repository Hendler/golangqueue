# Setup

## dev environment

assumes you have docker and go installed

    docker compose up --build -d

# run 
 
##  benchmarks

     go build -o ./benchmark-run ./benchmark/main.go
     ./benchmark-run -requests 20000 -clients 10 -concurrency 50 


 example bench output

    Benchmark Results:
    Total Duration: 9.879257427s
    Successful Requests: 20000
    Failed Requests: 0
    Average Request Time: 24.254079ms
    Maximum Request Time: 168.253088ms
    Minimum Request Time: 2.491334ms
    Requests/second: 2024.44

    Checking computation results...

    Status Check (elapsed time: 138ns):
    - Completed: 70
    - Pending: 7307
    - Not Found/Failed: 12623

    Status Check (elapsed time: 1m42.773404407s):
    - Completed: 1423
    - Pending: 18577
    - Not Found/Failed: 0
    ...

You can also see that round robin is working


    worker-1    | 2024/12/15 10:58:16 Processing request - CallerID: client-599137, Number: 37953193904633822419
    worker-1    | 2024/12/15 10:58:16 Processing request - CallerID: client-937365, Number: 89416368970745194523
    worker-1    | 2024/12/15 10:58:16 Processing request - CallerID: client-599137, Number: 8465375091434688124
    worker-1    | 2024/12/15 10:58:16 Processing request - CallerID: client-590854, Number: 32683341914465869449
    worker-1    | 2024/12/15 10:58:16 Processing request - CallerID: client-142032, Number: 50067340403705161807
    worker-1    | 2024/12/15 10:58:17 Processing request - CallerID: client-142032, Number: 87180621580426014420
    worker-1    | 2024/12/15 10:58:17 Processing request - CallerID: client-714009, Number: 24297971420810935712
    worker-1    | 2024/12/15 10:58:17 Processing request - CallerID: client-869768, Number: 31709446175617470195
    worker-1    | 2024/12/15 10:58:17 Processing request - CallerID: client-851991, Number: 28470785117835958864
    worker-1    | 2024/12/15 10:58:17 Processing request - CallerID: client-714009, Number: 23845271037315433349



## ISSUES

 - ~~FIXED, was using GMP, now using sympy the workers will often crash with out of memory errors data is safe.~~
 - you may need to stop and start docker so that NSQ topics get created. 
 
 ## cleanup to restart benches

    docker compose down 
    rm -rf volumes/redis/*
    go to http://localhost:4171/nodes to empty queues (and empty consuming channels)




# design decisions

Essentially, nsq is used to handle spikes and safety. Then a worker maintains a different Redis queue for consuming each caller's requests, to balance between them. Redis is also used to store state of processing and results. 

- chose go because it's easier than rust, faster than python, better memory and concurrency model than python
-  NSQ for the queue because it's easier to scale than redis for this use case across multiple machines and easy to integrate with go and easier to setup than kafka
-  redis because it's easy to setup and easy to use for fast storage and retrieval
- use string instead of int64 to let int parsing happen later for larger numbers

The most complext part of the app is balancing the worker service among callers since FIFO is not good enough when users have different numbers of jobs, some with massive spikes. I went down some rabit holes combining NSQ and redis, but in the end I found a simple solution.
We needed both NSQ for safety and scale, but Redis for speed and ease of use of state data.  

The definition of fairness could be autoscaled per user, or it could have limited resources per user. We assume an unknown limit and that each user is prioriotized based on the number of jobs they have in the queue, and simply do round robin. (I abandoned using redis sorted sets for this i'd have to build semaphores and transactions)

The bench generates primes to factor at   a random number between 10^6 and 10^20, but the numbers used can be larger because of the gmp library.
 
## ingestion steps

1. NSQ store jobs in queue immediately 
2. at the same time redis counts are incremented and a set of callerIDs is stored, indicating to each worker which channels it should 
3. an intermediate coordinator reads the queue and determines a priority of the request id, stores iit in redis. It then pops the top priority and adds it to the worker/priority queue. This can also be used to autoscale the workers.
4.  a worker reads from the priority queue, and when job is done store results in redis, a new results queue, which flushes every 5 seconds to disk, and we assume that is ok for persistence for now



# TODO
 
 - parse large string to big number format for prime factorization library
 - do actual prime factorization
 - send results to client

 ## client
 
  - generate random large numbers
  - hit server
  - check for response latency
  - wait one second and ask for update until result is provided
  - 10k concurrent requests
  - 100k concurrent requests
  - 1M concurrent requests
  - ouput data, output report

# simple improvements
- DRY code
- put haproxy in front of webserver to load balance - or if on cloud use other load balancer 
- better configuration management of ports across app
- use docker swarm to scale nsq , since I can use replicas attribute  
- for faster dev, bind source code and hot reload/rebuild go server
- cache results from factorization? Rainbow tables?
- don't requeue broken requests, place in different queue

## Production
- if staying with NSQ, there are limitations to channel counts - 100k or so because of partitioning
- observality across services with prometheus and grafana
- use dynamodb for its speed and persistence, triggers an event to lamda function - infitite scale
- build a deployment script and k8 pipeline to autoscale each component
- use a cloud service for hosted redis and sqs for example
 - event architecture to autoscale 
 - use EKS or ECS or even Lambda to autoscale workers
 - a more resilient system like SQS or Kafka may be better 

# Debugging

After 
    run docker compose up -d

Then you can see that nsq is up at http://localhost:4171/ and see nodes at http://localhost:4171/nodes to verify it's up.

BROKEN - to check redis is up and working, you can hit 
http://localhost:8004/

to check webserver is up and working, you can hit 
http://localhost:5555/compute/230432

To test input 

curl -X POST http://localhost:5555/compute \
  -H "Content-Type: application/json" \
  -H "X-Caller-ID: test-caller-1" \
  -d '{"number": "100000000000000000050700000000000000004563"}'

 
# work log

 - worked friday dec 13 from 6 to 7pm on getting server code and docker up 
 - worked saturday dec 14 from 11am to 1pm on queueing and balancing workers
 
 