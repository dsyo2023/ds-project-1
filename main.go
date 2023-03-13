package main

import (
	"dpasswd/fsm"
	"dpasswd/httpd"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"path"
	"time"

	"github.com/dgraph-io/badger/v2"
	"github.com/hashicorp/raft"
	raftboltdb "github.com/hashicorp/raft-boltdb"
)

var dataDir string
var httpPort int
var nodeID string
var raftPort int

func init() {
	flag.StringVar(&dataDir, "datadir", "data", "Data storage directory")
	flag.IntVar(&httpPort, "http-port", 3100, "HTTP server listen port")
	flag.StringVar(&nodeID, "id", "", "Node ID")
	flag.IntVar(&raftPort, "raft-port", 4200, "Raft RPC port")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options]\n", os.Args[0])
		flag.PrintDefaults()
	}
}

func main() {
	// Parse command line arguments
	flag.Parse()

	// Setup key-value database using badgerDB to be used as the FSM
	badgerOpts := badger.DefaultOptions(dataDir)
	badgerDB, err := badger.Open(badgerOpts)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := badgerDB.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "Error closing badgerDB: %s\n", err.Error())
		}
	}()
	kvFSM := fsm.NewRaftFSM(badgerDB)

	// Setup raft server
	raftCfg := raft.DefaultConfig()
	raftCfg.LocalID = raft.ServerID(nodeID)
	raftCfg.SnapshotThreshold = 1024

	logDB, err := raftboltdb.NewBoltStore(path.Join(dataDir, "log"))
	if err != nil {
		log.Fatal(err)
	}

	var raftLogCacheSize = 256
	cacheDB, err := raft.NewLogCache(raftLogCacheSize, logDB)
	if err != nil {
		log.Fatal(err)
	}

	var raftSnapShotRetain = 2
	ssDB, err := raft.NewFileSnapshotStore(dataDir, raftSnapShotRetain, os.Stdout)
	if err != nil {
		log.Fatal(err)
	}

	ipAddr := getIP()

	var raftBindAddr = fmt.Sprintf("%s:%d", ipAddr, raftPort)
	tcpAddr, err := net.ResolveTCPAddr("tcp", raftBindAddr)
	if err != nil {
		log.Fatal(err)
	}

	var raftMaxPool = 5
	var raftTcpTimeout = 5 * time.Second
	transport, err := raft.NewTCPTransport(raftBindAddr, tcpAddr, raftMaxPool, raftTcpTimeout, os.Stdout)
	if err != nil {
		log.Fatal(err)
	}

	r, err := raft.NewRaft(raftCfg, kvFSM, cacheDB, logDB, ssDB, transport)
	if err != nil {
		log.Fatal(err)
	}

	// Start the raft server
	r.BootstrapCluster(raft.Configuration{
		Servers: []raft.Server{
			{
				ID:      raft.ServerID(nodeID),
				Address: transport.LocalAddr(),
			},
		},
	})

	// Setup and start the HTTP Server
	var httpBindAddr = fmt.Sprintf(":%d", httpPort)
	s := httpd.NewHTTPServer(httpBindAddr, r, badgerDB)
	if err := s.Start(); err != nil {
		log.Fatal(err)
	}
}

func getIP() string {
	ifA, err := net.InterfaceAddrs()
	if err != nil {
		log.Fatal(err)
	}
	var ipAddr string
	for _, v := range ifA {
		if v.String() != "127.0.0.1/8" {
			ip, _, err := net.ParseCIDR(v.String())
			if err != nil {
				log.Fatal(err)
			}
			ipAddr = ip.String()
			break
		}
	}
	return ipAddr
}
