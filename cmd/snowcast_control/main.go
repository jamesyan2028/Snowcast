package main

import (
	"net"
	"fmt"
	"snowcast-jamesyan2028/pkg/protocol"
	"os"
	"log"
	"strconv"
)

func main() {
	if len(os.Args) != 4 {
		log.Fatalf("Usage: ./snowcast_control <server IP> <server port> <listener_port>")
		return
	}
	controlIP := os.Args[1]
	controlPort := os.Args[2]
	listenerPort, err := strconv.ParseUint(os.Args[3], 10, 16)
	if err != nil {
		log.Fatalf("Error parsing listener port: %v", err)
	}

	conn, err := net.Dial("tcp", controlIP+":"+controlPort)
	if err != nil {
		fmt.Println("error connecting to server: ", err)
		panic(err)
	}
	hello := &protocol.HelloMessage{
		CommandType: 0,
		UdpPort:     uint16(listenerPort),
	}
	serializedHello, err := hello.SerializeHello()
	if err != nil {
		fmt.Println("Error serializing hello message: ", err)
		panic(err)
	}
	_, err = conn.Write(serializedHello)
	if err != nil {
		fmt.Println("Error sending hello message to server: ", err)
		panic(err)
	}
	welcomeMessage, err := protocol.DeserializeWelcome(conn)
	if err != nil {
		fmt.Println("Error receiving welcome message from server: ", err)
		panic(err)
	}
	fmt.Printf("Welcome to Snowcast! The server has %d stations\n", welcomeMessage.NumStations)
	for {

	}

}