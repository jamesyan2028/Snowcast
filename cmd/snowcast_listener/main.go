package main

import (
	"net"
	"fmt"
	"os"
	"log"
)
func main() {
	if len(os.Args) != 2 {
		log.Fatalf("Usage: ./snowcast_listener <listener_port>")
		return
	}

	listenPort := os.Args[1]
	addr, err := net.ResolveUDPAddr("udp", ":"+listenPort)
	if err != nil {
		fmt.Println("Error Resolving Address: ", err)
		return
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		fmt.Println("Error Connecting to Port: ", err)
		return
	}

	buffer := make([]byte, 1500)

	for {
		n, err := conn.Read(buffer)
		if err != nil {
			fmt.Println("Error reading from UDP connection: ", err)
		}
		words := string(buffer[:n])
		fmt.Printf("%s", words)
	}

}