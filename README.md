# Setup

## dev environment

assumes you have docker 

    docker compose up -d

##  benchmarks



# design decisions



Essentially, nsq is used to handle spikes and safety. Then a worker maintains a different Redis queue for consuming each caller's requests, to balance between them. Redis is also used to store state of processing and results. 

- chose go because it's easier than rust, faster than python, better memory and concurrency model than python
-  NSQ for the queue because it's easier to scale than redis for this usecase and easier, easier to integrate with go and easier to setup than kafka
-  redis because it's easy to setup and easy to use
- use string instead of int64 to let int parsing happen later


# TODO

 - parse large string to big number
 - safer input
 - worker service that autoscales to handle nsq queue size
 - make worker service in docker
 - balanced load for reading
 
 ## benchmark
  - generate random large numbers
  - hit server
  - check for response latency
  - wait one second and ask for update until result is provided
  - 10k concurrent requests
  - 100k concurrent requests
  - 1M concurrent requests
  - ouput data, output report

# UNFINISHED

- put haproxy in front of webserver to load balance - or if on cloud use other load balancer 
- build a deployment script and k8 pipeline to autoscale each component
- use a cloud service for hosted redis and sqs for example
- better configuration management of ports across app
- use docker swarm to scale nsq , since I can use replicas attribute  
- for faster dev, bind source code and hot reload/rebuild go server

# Running the app

    run docker compose up -d

Then you can see that nsq is up at http://localhost:4171/ and see nodes at http://localhost:4171/nodes to verify it's up.

to check redis is up and working, you can hit 
http://localhost:8004/

to check webserver is up and working, you can hit 
http://localhost:5555/compute/230432

To test input 

curl -X POST http://localhost:5555/compute \
  -H "Content-Type: application/json" \
  -H "X-Caller-ID: test-caller-1" \
  -d '{"number": "100000000000000000050700000000000000004563"}'

# To benchmark

 

# work log

 - worked friday dec 13 from 6 to 7pm
 