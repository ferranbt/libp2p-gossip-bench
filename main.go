package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/ferranbt/libp2p-gossip-bench/latency"
	"github.com/ferranbt/libp2p-gossip-bench/network"
	"github.com/ferranbt/libp2p-gossip-bench/proto"
	"github.com/ferranbt/libp2p-gossip-bench/support"
	"github.com/golang/protobuf/ptypes/any"
	"github.com/hashicorp/go-hclog"

	ma "github.com/multiformats/go-multiaddr"
)

func main() {
	rand.Seed(time.Now().Unix())

	size := 200
	gossipSize := 100
	msgSize := 1024
	maxPeers := 10

	m := &Mesh{
		latency:  readLatency(),
		logger:   hclog.New(&hclog.LoggerOptions{Output: os.Stdout}),
		port:     30000,
		maxPeers: maxPeers,
	}
	m.manager = &latency.Manager{
		QueryNetwork: m.queryLatencies,
	}

	for i := 0; i < size; i++ {
		m.runServer("srv_" + strconv.Itoa(i))
	}

	// join them in a line
	var wg sync.WaitGroup
	for i := 0; i < size-1; i++ {
		wg.Add(1)

		go func(i int) {
			m.join("srv_"+strconv.Itoa(i), "srv_"+strconv.Itoa(i+1))
			wg.Done()
		}(i)
	}
	wg.Wait()

	time.Sleep(1 * time.Minute)

	// m.waitForPeers(3)

	for i := 0; i < gossipSize; i++ {
		m.gossip("srv_"+strconv.Itoa(i), msgSize)
	}
	for i := 0; i < gossipSize; i++ {
		m.gossip("srv_"+strconv.Itoa(i), msgSize)
	}

	time.Sleep(1 * time.Second)

	compute := func(show bool) {
		total := 0
		for _, p := range m.servers {
			if show {
				fmt.Println(p.city, p.numTopics(), p.NumPeers())
			}
			total += p.numTopics()
		}
		fmt.Println(total / len(m.servers))
	}

	compute(false)

	time.Sleep(1 * time.Second)

	compute(false)

	time.Sleep(1 * time.Second)

	compute(false)

	// 50 nodos, full, 1 second 95%
	//
	// handleSignals()
}

func handleSignals() {
	signalCh := make(chan os.Signal, 4)
	signal.Notify(signalCh, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)

	<-signalCh
	os.Exit(0)
}

type Mesh struct {
	lock sync.Mutex

	latency  *LatencyData
	logger   hclog.Logger
	servers  map[string]*server
	port     int
	manager  *latency.Manager
	maxPeers int
}

func (m *Mesh) waitForPeers(min int64) {
	check := func() bool {
		for _, p := range m.servers {
			if p.NumPeers() < min {
				return false
			}
		}
		return true
	}
	for {
		if check() {
			return
		}
	}
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

func (m *Mesh) gossip(from string, size int) {
	buf := make([]byte, size)
	rand.Read(buf)

	m.servers[from].topic.Publish(&proto.Txn{
		From: from,
		Raw: &any.Any{
			Value: buf,
		},
	})
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

	n := rand.Int() % len(m.latency.SourcesList)
	city := m.latency.SourcesList[n].Name

	config := network.DefaultConfig()
	config.Transport = m.manager.Transport()
	config.City = city
	config.MaxPeers = uint64(m.maxPeers)

	config.Addr = &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: m.port}
	m.port++

	m.servers[name] = newServer(m.logger.Named(name), config, city)
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

	topicsLock sync.Mutex
	topics     map[string]struct{}

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

	s := &server{
		config: config,
		Server: srv,
		topic:  topic,
		city:   city,
		topics: map[string]struct{}{},
	}
	topic.Subscribe(func(obj interface{}) {
		s.topicsLock.Lock()
		defer s.topicsLock.Unlock()

		msg := obj.(*proto.Txn)
		hash := hashit(msg.Raw.Value)
		s.topics[hash] = struct{}{}
	})
	return s
}

func (s *server) numTopics() int {
	s.topicsLock.Lock()
	defer s.topicsLock.Unlock()

	return len(s.topics)
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

func hashit(b []byte) string {
	h := sha256.New()
	h.Write(b)
	dst := h.Sum(nil)
	return hex.EncodeToString(dst)
}
