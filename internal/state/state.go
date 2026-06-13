package state

import (
	"fmt"
	"net"
	"sync"

	pb "snowcast-jamesyan2028/pkg/protocol"
)

// ClientInfo holds per-client session state.
type ClientInfo struct {
	UDPAddr     *net.UDPAddr
	CurrStation int
	EventChan   chan *pb.ServerEvent
}

// Station holds subscribers for one station.
type Station struct {
	Mutex   sync.Mutex
	Clients []*ClientInfo
	Name    string
}

var (
	CurrentClients = make(map[string]*ClientInfo)
	ClientMutex    sync.Mutex
	StationList    []Station
)

// InitStations creates station metadata from file paths.
func InitStations(files []string) {
	StationList = make([]Station, len(files))
	for i, path := range files {
		StationList[i].Name = path
	}
}

// ClientKey builds the map key for a client.
func ClientKey(ip string, udpPort uint32) string {
	return fmt.Sprintf("%s:%d", ip, udpPort)
}

// ApplyHandshake registers a new client.
func ApplyHandshake(ip string, udpPort uint32) (string, error) {
	key := ClientKey(ip, udpPort)

	ClientMutex.Lock()
	defer ClientMutex.Unlock()

	if _, exists := CurrentClients[key]; exists {
		return key, fmt.Errorf("client already connected: %s", key)
	}

	udpAddr := &net.UDPAddr{
		IP:   net.ParseIP(ip),
		Port: int(udpPort),
	}
	client := &ClientInfo{
		UDPAddr:     udpAddr,
		CurrStation: -1,
		EventChan:   make(chan *pb.ServerEvent, 100),
	}
	CurrentClients[key] = client
	return key, nil
}

// RollbackHandshake removes a client registered by ApplyHandshake.
func RollbackHandshake(ip string, udpPort uint32) {
	key := ClientKey(ip, udpPort)
	ClientMutex.Lock()
	delete(CurrentClients, key)
	ClientMutex.Unlock()
}

// ApplySetStation moves a client to a new station. Caller must validate stationNum.
func ApplySetStation(clientKey string, stationNum int) (songName string, err error) {
	ClientMutex.Lock()
	client, found := CurrentClients[clientKey]
	ClientMutex.Unlock()
	if !found {
		return "", fmt.Errorf("client not found: %s", clientKey)
	}

	removeFromCurrentStation(client)

	ClientMutex.Lock()
	client.CurrStation = stationNum
	ClientMutex.Unlock()

	station := &StationList[stationNum]
	station.Mutex.Lock()
	station.Clients = append(station.Clients, client)
	station.Mutex.Unlock()

	return station.Name, nil
}

// RollbackSetStation restores a client to previousStation (-1 means not on any station).
func RollbackSetStation(clientKey string, previousStation int) {
	ClientMutex.Lock()
	client, found := CurrentClients[clientKey]
	ClientMutex.Unlock()
	if !found {
		return
	}

	removeFromCurrentStation(client)

	if previousStation >= 0 && previousStation < len(StationList) {
		station := &StationList[previousStation]
		station.Mutex.Lock()
		station.Clients = append(station.Clients, client)
		station.Mutex.Unlock()
	}

	ClientMutex.Lock()
	client.CurrStation = previousStation
	ClientMutex.Unlock()
}

// ApplyDisconnect removes a client from all structures.
func ApplyDisconnect(clientKey string) error {
	ClientMutex.Lock()
	client, found := CurrentClients[clientKey]
	if !found {
		ClientMutex.Unlock()
		return fmt.Errorf("client not found: %s", clientKey)
	}
	delete(CurrentClients, clientKey)
	ClientMutex.Unlock()

	removeFromCurrentStation(client)
	close(client.EventChan)
	return nil
}

// RollbackDisconnect re-registers a client after a failed replication.
func RollbackDisconnect(clientKey string, client *ClientInfo) {
	ClientMutex.Lock()
	CurrentClients[clientKey] = client
	ClientMutex.Unlock()

	if client.CurrStation >= 0 && client.CurrStation < len(StationList) {
		station := &StationList[client.CurrStation]
		station.Mutex.Lock()
		station.Clients = append(station.Clients, client)
		station.Mutex.Unlock()
	}
}

// ApplyWalEntry applies a replicated WAL entry (used by backup and replay).
func ApplyWalEntry(entry *pb.WalEntry) error {
	switch op := entry.Op.(type) {
	case *pb.WalEntry_Handshake:
		_, err := ApplyHandshake(op.Handshake.ClientIp, op.Handshake.UdpPort)
		return err
	case *pb.WalEntry_SetStation:
		key := ClientKey(op.SetStation.ClientIp, op.SetStation.UdpPort)
		_, err := ApplySetStation(key, int(op.SetStation.StationNumber))
		return err
	case *pb.WalEntry_Disconnect:
		key := ClientKey(op.Disconnect.ClientIp, op.Disconnect.UdpPort)
		return ApplyDisconnect(key)
	default:
		return fmt.Errorf("unknown wal op")
	}
}

func removeFromCurrentStation(client *ClientInfo) {
	if client.CurrStation == -1 {
		return
	}
	station := &StationList[client.CurrStation]
	station.Mutex.Lock()
	for i, c := range station.Clients {
		if c == client {
			station.Clients[i] = station.Clients[len(station.Clients)-1]
			station.Clients = station.Clients[:len(station.Clients)-1]
			break
		}
	}
	station.Mutex.Unlock()
}

// RemoveFromCurrentStation is exported for primary gRPC disconnect path compatibility.
func RemoveFromCurrentStation(client *ClientInfo) {
	removeFromCurrentStation(client)
}

// GetClient returns a client by key.
func GetClient(clientKey string) (*ClientInfo, bool) {
	ClientMutex.Lock()
	defer ClientMutex.Unlock()
	c, ok := CurrentClients[clientKey]
	return c, ok
}

// NumStations returns the station count.
func NumStations() int {
	return len(StationList)
}
