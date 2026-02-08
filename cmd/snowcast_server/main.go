package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"snowcast-jamesyan2028/pkg/protocol"
	"sync"
	"time"
)

//clientMap := make(map[string]int)

type WelcomeInfo struct {
	conn net.Conn
	files []string
}

type ClientInfo struct {
	tcpConn net.Conn
	udpAddr *net.UDPAddr
	currStation int
}

type Station struct {
	mutex sync.Mutex
	clients []*ClientInfo
}

var (
	currentClients = make(map[net.Conn]*ClientInfo)
	clientMutex sync.Mutex
	stationList []Station
)

func main() {
	if len(os.Args) < 3 {
		log.Fatalf("Usage: ./snowcast_server <listen port> <file0> [file1] ...")
		return
	}
	port := os.Args[1]
	files := os.Args[2:]
	
	addr, err := net.ResolveTCPAddr("tcp", "127.0.0.1:"+port)
	if err != nil {
		fmt.Println("Error resolving address: ", err)
		panic(err)
	}

	listenConn, err := net.ListenTCP("tcp", addr)
	if err != nil {
		fmt.Println("Error starting TCP Listener: ", err)
		panic(err)
	}

	numStations := len(files)
	stationList = make([]Station, numStations)

	udpAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:" + port)
	if err != nil {
		fmt.Printf("Error creating UDP port on server: %s", err)
		return
	}
	udpConn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		fmt.Printf("Error starting udp port on server: %s", err)
	}
	for i, filename := range files {
		go streamStation(i, filename, udpConn)
	}

	for {
		tcpConn, err := listenConn.AcceptTCP()
		if err != nil {
			fmt.Println("Error accepting connection: ", err)
			continue
		}

		clientInfo := &WelcomeInfo{
			conn: tcpConn,
			files: files,
		}

		go handleConnection(clientInfo)
	}

}


func handleConnection(ci *WelcomeInfo) {
	//clientMap[conn.RemoteAddr().String()] = -1
	conn := ci.conn
	files := ci.files
	defer conn.Close()
	hello, err := protocol.DeserializeHello(conn)
	if err != nil {
		fmt.Println("Error reading message from client ", conn.RemoteAddr().String(), ": ", err)
		return
	}

	tcpAddr := conn.RemoteAddr().(*net.TCPAddr)
	udpAddr := &net.UDPAddr{
		IP: tcpAddr.IP,
		Port: int(hello.UdpPort),
	}

	currClient := &ClientInfo{
		tcpConn: conn,
		udpAddr: udpAddr,
		currStation: -1,
	}

	currentClients[conn] = currClient

	//Add logic to make sure the message is valid later

	welcome := &protocol.WelcomeMessage{
		ReplyType:   2,
		NumStations: uint16(len(files)),
	}
	serializedWelcome, err := welcome.SerializeWelcome()
	if err != nil {
		fmt.Println("Error serializing welcome message to client ", conn.RemoteAddr().String(), ": ", err)
		return
	}
	_, err = conn.Write(serializedWelcome)
	if err != nil {
		fmt.Println("Error sending welcome message to client ", conn.RemoteAddr().String(), ": ", err)
		return
	}
	fmt.Println("Client Connected: ", conn.RemoteAddr().String())
	for {
		msg, err := protocol.DeserializeClientMessage(conn)
		if err != nil {
			fmt.Printf("Error reading client message: %s", err)
			continue
		}
		switch msgType := msg.(type) {
		case *protocol.WelcomeMessage:
			response := &protocol.InvalidCommandMessage{
				ReplyType: 4,
				ReplyStringSize: 32,
				ReplyString: "Cannot send second Hello Message",
			}
			serializedMsg, err := protocol.SerializeInvalidMessage(response)
			if err != nil {
				fmt.Printf("Error serializing invalid message: %s", err)
				continue
			}
			conn.Write(serializedMsg)
			conn.Close()
			return
		case *protocol.SetStationMessage:
			newStationNumber := msgType.StationNumber
			if int(newStationNumber) < len(stationList) && int(newStationNumber) >= 0 {
				changeStation(conn, int(newStationNumber))
			} else {
				response := &protocol.InvalidCommandMessage{
					ReplyType: 4,
					ReplyStringSize: 22,
					ReplyString: "Invalid Station Number",
				}
				serializedMsg, err := protocol.SerializeInvalidMessage(response)
				if err != nil {
					fmt.Printf("Error serializing invalid message: %s", err)
					continue
				}
				conn.Write(serializedMsg)
				conn.Close()
				return
			}
		default:
			response := &protocol.InvalidCommandMessage{
				ReplyType: 4,
				ReplyStringSize: 20,
				ReplyString: "Invalid Message Type",
			}
			serializedMsg, err := protocol.SerializeInvalidMessage(response)
			if err != nil {
				fmt.Printf("Error serializing invalid message: %s", err)
				continue
			}
			conn.Write(serializedMsg)
			conn.Close()
			return
		}
	}
	
}

func changeStation(conn net.Conn, newStationIndex int) {
	clientMutex.Lock()
	client := currentClients[conn]
	clientMutex.Unlock()

	
	if client.currStation != -1 {
		oldStation := client.currStation
		stationList := &stationList[oldStation]
		stationList.mutex.Lock()
		for i, c := range stationList.clients {
			if c == client {
				stationList.clients[i] = stationList.clients[len(stationList.clients) - 1]
				stationList.clients = stationList.clients[:len(stationList.clients) - 1]
				break
			}
		}
		stationList.mutex.Unlock()
	}

	clientMutex.Lock()
	client.currStation = newStationIndex
	clientMutex.Unlock()

	newStationList := &stationList[newStationIndex]
	newStationList.mutex.Lock()
	newStationList.clients = append(newStationList.clients, client)
	newStationList.mutex.Unlock()
}

func streamStation(id int, filename string, udpConn *net.UDPConn) {
	file, err := os.Open(filename)
	if err != nil {
		fmt.Printf("Error opening file: " + "%s", filename)
	}
	defer file.Close()
	buffer := make([]byte, 1500)
	ticker := time.NewTicker(91550 * time.Microsecond)
	defer ticker.Stop()
	for range ticker.C {
		n, err := file.Read(buffer)
		if n > 0 {
			currStation := &stationList[id]
			currStation.mutex.Lock()
			for _, client := range currStation.clients {
				udpConn.WriteToUDP(buffer[:n], client.udpAddr)
			}
			currStation.mutex.Unlock()
		}

		if err != nil {
			if err == io.EOF {
				file.Seek(0, 0)
				announceMsg := &protocol.AnnounceMessage{
					ReplyType: 3,
					SongNameSize: uint8(len(filename)),
					SongName: filename,
				}
				serializedMsg, err := protocol.SerializeAnnounce(announceMsg)
				if err != nil {
					fmt.Printf("Error serializing announce message: " + "%s", err)
				}
				currStation := &stationList[id]
				currStation.mutex.Lock()
				for _, client := range currStation.clients {
					client.tcpConn.Write(serializedMsg)
				}
				currStation.mutex.Unlock()
			} else {
				fmt.Printf("Error reading file: %s", err)
			}
		}

	}



}