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
	"bufio"
	"strings"
	"errors"
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
	name string
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
	for i, path := range files {
		stationList[i].name = path
 		go streamStation(i, path, udpConn)
	}


	go func() {
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
	}()

	handleUserInput()

}


func handleConnection(ci *WelcomeInfo) {
	//SETUP CODE AND HANDSHAKE
	conn := ci.conn
	files := ci.files
	defer deleteClient(conn, true)

	conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	msg, err := protocol.DeserializeClientMessage(conn, true)
	if err != nil {
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			fmt.Printf("Timeout from client %s, disconnecting", conn.RemoteAddr())
			conn.Close()
			return
		} else {
			fmt.Println("Error reading message from client ", conn.RemoteAddr().String(), ": ", err)
			conn.Close()
			return
		}
	}
	conn.SetReadDeadline(time.Time{})
	hello, ok := msg.(*protocol.HelloMessage)
	if !ok {
		fmt.Printf("Client Did Not Send Hello as First Message, Closing Connection")
		conn.Close()
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

	//END SETUP CODE: ON TO MAIN HANDLING REQUESTS LOOP
	for {
		msg, err := protocol.DeserializeClientMessage(conn, false)
		if err != nil || errors.Is(err, net.ErrClosed) {
			if err == io.EOF {
				fmt.Printf("Client %s disconnected, closing connection\n", conn.RemoteAddr())
				return
			} else if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				fmt.Printf("Timeout error from client %s, clsosing connection\n", conn.RemoteAddr())
				return
			}
			fmt.Printf("Error reading client message: %s", err)
			return
		}
		switch msgType := msg.(type) {
		case *protocol.WelcomeMessage:
			fmt.Printf("Received Second Welcome Message from client %s, terminating connection\n", conn.RemoteAddr())
			response := &protocol.InvalidCommandMessage{
				ReplyType: 4,
				ReplyStringSize: 32,
				ReplyString: "Cannot send second Hello Message",
			}
			serializedMsg, err := protocol.SerializeInvalidMessage(response)
			if err != nil {
				fmt.Printf("Error serializing invalid message: %s\n", err)
				continue
			}
			conn.Write(serializedMsg)
			fmt.Printf("Send Invalid Command Message to client %s, closing connection\n", conn.RemoteAddr())
			return
		case *protocol.SetStationMessage:
			fmt.Printf("Received Message To Change Conection %s to station %d\n", conn.RemoteAddr(), msgType.StationNumber)
			newStationNumber := msgType.StationNumber
			if int(newStationNumber) < len(stationList) && int(newStationNumber) >= 0 {
				changeStation(conn, int(newStationNumber))
			} else {
				fmt.Printf("Receieved Invalid Command from Client %s, Request To Change to Channel %d, Channel Does Not Exist\n", conn.RemoteAddr(), msgType.StationNumber)
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
				fmt.Printf("Sent Invalid Command Message to Client %s, Closing Connection\n", conn.RemoteAddr())
				return
			}
		default:
			fmt.Printf("Receieved Unknown Command from Client %s, Closing Connection\n", conn)
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
			fmt.Printf("Sent Invalid Command Message to Client %s, Closing Connection\n", conn.RemoteAddr())
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

	announceMsg := &protocol.AnnounceMessage{
		ReplyType: 3,
		SongNameSize: uint8(len(newStationList.name)),
		SongName: newStationList.name,
	}

	serializedMsg, err := protocol.SerializeAnnounce(announceMsg)
	if err != nil {
		fmt.Printf("Error serializing announce message: %s", err)
		return
	}
	client.tcpConn.Write(serializedMsg)
}

func deleteClient(conn net.Conn, lockMutex bool) {
	if lockMutex {
		clientMutex.Lock()
	}
	client, found := currentClients[conn]
	if !found {
		fmt.Printf("No client Found: %s", conn.RemoteAddr())
		if lockMutex {
			clientMutex.Unlock()
		}
		return
	}
	delete(currentClients, conn)
	if lockMutex {
		clientMutex.Unlock()
	}


	//Remove from active listeners array
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
	conn.Close()
}
func streamStation(id int, filename string, udpConn *net.UDPConn) {
	file, err := os.Open(filename)
	if err != nil {
		fmt.Printf("Error opening file: " + "%s", filename)
		return
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

func handleUserInput() {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		input := scanner.Text()
		input = strings.TrimSpace(input)

		words := strings.Fields(input)
		if len(words) == 0 {
			continue
		}
		switch words[0] {
		case "p":
			if len(words) == 1 {
				fmt.Print(formatStationString())
			} else if len(words) == 2 {
				fileName := words[1]
				err := os.WriteFile(fileName, []byte(formatStationString()), 0644)
				if err != nil {
					fmt.Printf("Error writing output to file: %s", err)
				}
			} else {
				fmt.Printf("Invalid Command Type\n")
			}
		case "q":
			//May be an error here if deleting while iterating through
			clientMutex.Lock()
			for conn, _ := range currentClients {
				response := &protocol.InvalidCommandMessage{
					ReplyType: 4, 
					ReplyStringSize: 43,
					ReplyString: "Server Shutting Down, Close All Connections", 
				}
				serializedMsg, err := protocol.SerializeInvalidMessage(response)
				if err != nil {
					fmt.Printf("Error shutting down connection: %s\n", err)
				}
				conn.Write(serializedMsg)
				deleteClient(conn, false)
				fmt.Print("what the sigma")
			}
			clientMutex.Unlock()
			os.Exit(0)
			return
		default:
			fmt.Printf("Invalid command\n")
		} 

	}
}

func formatStationString () string {
	var builder strings.Builder
	clientMutex.Lock()
	defer clientMutex.Unlock()

	for i := range stationList {
		station := &stationList[i]
		station.mutex.Lock()
		builder.WriteString(fmt.Sprintf("%d,%s", i, station.name))

		for _, client := range station.clients {
			builder.WriteString(fmt.Sprintf(",%s:%d", client.udpAddr.IP.String(), client.udpAddr.Port))
		}
		builder.WriteString("\n")
		station.mutex.Unlock()
	}
	return builder.String()
}