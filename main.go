package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/ferranbt/libp2p-gossip-bench/latency"
	"github.com/ferranbt/libp2p-gossip-bench/network"
	"github.com/ferranbt/libp2p-gossip-bench/proto"
	"github.com/ferranbt/libp2p-gossip-bench/support"
	"github.com/hashicorp/go-hclog"

	ma "github.com/multiformats/go-multiaddr"
)

func main() {
	rand.Seed(time.Now().Unix())

	m := &Mesh{
		latency: readLatency(),
		logger:  hclog.New(&hclog.LoggerOptions{Output: os.Stdout}),
		port:    30000,
	}
	m.manager = &latency.Manager{
		QueryNetwork: m.queryLatencies,
	}

	defer m.Stop()

	m.runServer("a")
	m.runServer("b")

	m.join("a", "b")
	m.gossip("a")

	time.Sleep(5 * time.Second)
}

type Mesh struct {
	lock sync.Mutex

	latency *LatencyData
	logger  hclog.Logger
	servers map[string]*server
	port    int
	manager *latency.Manager
}

func (m *Mesh) findPeerByPort(port string) *server {
	for _, p := range m.servers {
		if strconv.Itoa(p.config.Addr.Port) == port {
			return p
		}
	}
	panic("bad")
}

func (m *Mesh) queryLatencies(laddr, raddr ma.Multiaddr) support.Network {
	m.lock.Lock()
	defer m.lock.Unlock()

	laddrPort, err := laddr.ValueForProtocol(ma.P_TCP)
	if err != nil {
		panic(err)
	}
	raddrPort, err := raddr.ValueForProtocol(ma.P_TCP)
	if err != nil {
		panic(err)
	}

	laddrSrv := m.findPeerByPort(laddrPort)
	raddrSrv := m.findPeerByPort(raddrPort)

	latency := m.latency.FindLatency(laddrSrv.city, raddrSrv.city)

	nn := support.Network{
		Kbps:    20 * 1024, // should I change this?
		Latency: latency,
		MTU:     1500,
	}
	return nn
}

func (m *Mesh) gossip(from string) {
	m.servers[from].topic.Publish(&proto.Txn{})
}

func (m *Mesh) join(fromID, toID string) error {
	from := m.servers[fromID]
	to := m.servers[toID]
	return from.Join(to.AddrInfo(), 5*time.Second)
}

func (m *Mesh) runServer(name string) error {
	if m.servers == nil {
		m.servers = map[string]*server{}
	}

	config := network.DefaultConfig()
	config.Transport = m.manager.Transport()

	config.Addr = &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: m.port}
	m.port++

	n := rand.Int() % len(m.latency.SourcesList)

	m.servers[name] = newServer(m.logger.Named(name), config, m.latency.SourcesList[n].Name)
	return nil
}

func (m *Mesh) Stop() {
	for _, srv := range m.servers {
		srv.Close()
	}
}

type server struct {
	config *network.Config

	*network.Server

	topic *network.Topic

	city string
}

func newServer(logger hclog.Logger, config *network.Config, city string) *server {
	srv, err := network.NewServer(logger, config)
	if err != nil {
		panic(err)
	}

	// create a gossip protocol
	topic, err := srv.NewTopic("/proto1", &proto.Txn{})
	if err != nil {
		panic(err)
	}
	topic.Subscribe(func(obj interface{}) {
		fmt.Println("- topic -")
	})

	return &server{
		config: config,
		Server: srv,
		topic:  topic,
		city:   city,
	}
}

// read latencies

type LatencyData struct {
	PingData map[string]map[string]struct {
		Avg string
	}
	SourcesList []struct {
		Id   string
		Name string
	}
	sources map[string]string
}

func (l *LatencyData) FindLatency(from, to string) time.Duration {
	fromID := l.sources[from]
	toID := l.sources[to]

	dur, err := time.ParseDuration(l.PingData[fromID][toID].Avg + "ms")
	if err != nil {
		panic(err)
	}
	return dur
}

func readLatency() *LatencyData {
	data, err := ioutil.ReadFile("./data.json")
	if err != nil {
		panic(err)
	}

	var latencyData LatencyData
	if err := json.Unmarshal(data, &latencyData); err != nil {
		panic(err)
	}

	latencyData.sources = map[string]string{}
	for _, i := range latencyData.SourcesList {
		latencyData.sources[i.Name] = i.Id
	}
	return &latencyData
}
