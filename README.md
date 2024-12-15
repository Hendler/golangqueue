# Setup

## dev environment

assumes you have docker 

    docker compose up -d

##  benchmarks

- basic 


# design decisions

Essentially, nsq is used to handle spikes and safety. Then a worker maintains a different Redis queue for consuming each caller's requests, to balance between them. Redis is also used to store state of processing and results. 

- chose go because it's easier than rust, faster than python, better memory and concurrency model than python
-  NSQ for the queue because it's easier to scale than redis for this use case across multiple machines and easy to integrate with go and easier to setup than kafka
-  redis because it's easy to setup and easy to use for fast storage and retrieval
- use string instead of int64 to let int parsing happen later for larger numbers

The most complext part of the app is balancing the worker service among callers since FIFO is not good enough when users have different numbers of jobs, some with massive spikes. I went down some rabit holes combining NSQ and redis, but in the end I found a simple solution.
We needed both NSQ for safety and scale, but Redis for speed and ease of use of state data.  

The definition of fairness could be autoscaled per user, or it could have limited resources per user. We assume an unknown limit and that each user is prioriotized based on the number of jobs they have in the queue, and simply do round robin. (I abandoned using redis sorted sets for this i'd have to build semaphores and transactions)
 
## ingestion steps

1. NSQ store jobs in queue immediately 
2. at the same time redis counts are incremented and a set of callerIDs is stored, indicating to each worker which channels it should 
3. an intermediate coordinator reads the queue and determines a priority of the request id, stores iit in redis. It then pops the top priority and adds it to the worker/priority queue. This can also be used to autoscale the workers.
4.  a worker reads from the priority queue, and when job is done store results in redis, a new results queue, which flushes every 5 seconds to disk, and we assume that is ok for persistence for now



# TODO
 - make worker service in docker
 - parse large string to big number
 - fix nested logic that's broken

 
 ## benchmark
  - generate random large numbers
  - hit server
  - check for response latency
  - wait one second and ask for update until result is provided
  - 10k concurrent requests
  - 100k concurrent requests
  - 1M concurrent requests
  - ouput data, output report

# simple improvements

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
 
 