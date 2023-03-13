# ds-project

This is a simple CLI password manager application built on top of distributed key-value database.
The [Raft consensus algorithm](https://raft.github.io/) is used to implement the replication of data between the nodes and to achieve fault-tolerance.
[Hashicorp's raft](https://github.com/hashicorp/raft) provides the Golang implementation of the raft protocol
and [BadgerDB](https://github.com/dgraph-io/badger) is used as a key-value database to store the data at the server nodes.

## Usage

For building the application you'll first need [Go](https://go.dev/doc/install) version 1.19+ installed.
Then, for running the raft servers you'll need [Docker](https://docs.docker.com/engine/install/) and [Docker compose](https://docs.docker.com/compose/install/).

Then, to start the raft cluster (three servers nodes will be created by default):
```sh
docker compose build --pull
docker compose up
```

From other terminal window we can then use the HTTP API (at port 380x) exposed by the servers to manage the cluster.
```sh
# To see information about nodes
curl 'http://localhost:3801/raft/stats'
curl 'http://localhost:3802/raft/stats'
curl 'http://localhost:3803/raft/stats'
```

To set up the replication we must inform the nodes about other nodes in the network.
```sh
# Here node01 will be chosen as the leader node and node02 and node03 will be chosen as followers.
curl -X POST 'http://localhost:3801/raft/join' -d '{"node_id": "node02", "raft_address": "node02:2802"}' -H 'content-type: application/json'
curl -X POST 'http://localhost:3801/raft/join' -d '{"node_id": "node03", "raft_address": "node03:2803"}' -H 'content-type: application/json'
```

Now that we have our distributed key-value service running we can use the application to manage our passwords.
To build the client application:
```sh
go build -o app client/app.go
```
Then running the client application:
```sh
./app --addr localhost:3801
```