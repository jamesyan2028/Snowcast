package main

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"os"
	"snowcast-jamesyan2028/pkg/protocol"
	"strconv"
	"strings"
	"unicode"
	"io"
	"errors"
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
		return
	}

	conn, err := net.Dial("tcp", controlIP+":"+controlPort)
	if err != nil {
		fmt.Println("error connecting to server: ", err)
		return
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

	exit := make(chan bool)

	go func() {
		handleServerEvent(conn)
		exit <- true
	}()

	go func() {
		handleUserInput(conn)
		exit <- true
	}()

	<- exit

	conn.Close()
}

func handleServerEvent(conn net.Conn) {
	for {
		msg, err := protocol.DeserializeServerMessage(conn)
		if err != nil {
			if err == io.EOF || errors.Is(err, net.ErrClosed){
				return
			}
			fmt.Println("Error receiving server message: ", err)
			return
		}
		switch msgType := msg.(type) {
		case *protocol.WelcomeMessage:
			fmt.Printf("Welcome to Snowcast! The server has %d stations\n", msgType.NumStations)
		case *protocol.AnnounceMessage:
			fmt.Printf("New Song Announced: %s\n", msgType.SongName)
		case *protocol.InvalidCommandMessage:
			fmt.Printf("Invalid Command: %s\n", msgType.ReplyString)
			return
		default:
			fmt.Printf("Unknown message type received: %s\n", msg)
			return
		}
	}
}

func handleUserInput(conn net.Conn) {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		input := scanner.Text()
		input = strings.TrimSpace(input)
		switch {
		case input == "q":
			return
		case input == "":
			continue
		case checkOnlyNumbers(input):
			stationNum, err := strconv.ParseUint(input, 10, 16)
			if err != nil {
				fmt.Printf("Error Parsing Station Number: %s\n", err)
			}

			setStationMessage := &protocol.SetStationMessage{
				CommandType: 1,
				StationNumber: uint16(stationNum),
			}

			serializedMessage, err := protocol.SerializeSetStation(setStationMessage)
			if err != nil {
				fmt.Printf("Error serializing Message: " + "%s", err)
				continue
			}
			conn.Write(serializedMessage)
		default:
			fmt.Printf("Invalid Command: %s\n", input)
			continue
		}
	}
}

func checkOnlyNumbers(s string)bool {
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	} 
	return true
}