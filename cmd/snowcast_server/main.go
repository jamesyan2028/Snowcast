package main

import (
	"fmt"
	"net"
	"snowcast-jamesyan2028/pkg/protocol"
	"os"
	"log"
)

//clientMap := make(map[string]int)

type ClientInfo struct {
	Conn net.Conn
	files []string
}

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

	for {
		tcpConn, err := listenConn.AcceptTCP()
		if err != nil {
			fmt.Println("Error accepting connection: ", err)
			panic(err)
		}

		clientInfo := &ClientInfo{
			Conn: tcpConn,
			files: files,
		}

		go handleConnection(clientInfo)
	}

}


func handleConnection(ci *ClientInfo) {
	//clientMap[conn.RemoteAddr().String()] = -1
	conn := ci.Conn
	files := ci.files
	defer conn.Close()
	_, err := protocol.DeserializeHello(conn)
	if err != nil {
		fmt.Println("Error reading message from client ", conn.RemoteAddr().String(), ": ", err)
		return
	}
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
		
	}
	
}