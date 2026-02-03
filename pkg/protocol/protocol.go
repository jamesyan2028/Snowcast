package protocol
import (
	"encoding/binary"
	"bytes"
	"net"
)

type HelloMessage struct {
    CommandType uint8
    UdpPort     uint16
}

type WelcomeMessage struct {
    ReplyType   uint8
    NumStations uint16
}

const (
	HelloMessageSize   = 3
	WelcomeMessageSize = 3
)

func (m *HelloMessage) SerializeHello() ([]byte, error) {
	buf := new(bytes.Buffer)
	err := binary.Write(buf, binary.BigEndian, m.CommandType)
	if err != nil {
		return nil, err
	}
	err = binary.Write(buf, binary.BigEndian, m.UdpPort) 
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (m *WelcomeMessage) SerializeWelcome() ([]byte, error) {
	buf := new(bytes.Buffer)
	err := binary.Write(buf, binary.BigEndian, m.ReplyType)
	if err != nil {
		return nil, err
	}
	err = binary.Write(buf, binary.BigEndian, m.NumStations) 
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func DeserializeHello(conn net.Conn) (*HelloMessage, error) {
	buffer := make([]byte, HelloMessageSize)
	_, err := conn.Read(buffer)
	if err != nil {
		return nil, err
	}
	msg := &HelloMessage{
		CommandType: buffer[0],
		UdpPort:     uint16(binary.BigEndian.Uint16(buffer[1:3])),
	}
	return msg, nil
}

func DeserializeWelcome(conn net.Conn) (*WelcomeMessage, error) {
	buffer := make([]byte, WelcomeMessageSize)
	_, err := conn.Read(buffer)
	if err != nil {
		return nil, err
	}
	msg := &WelcomeMessage{
		ReplyType:   buffer[0],
		NumStations: uint16(binary.BigEndian.Uint16(buffer[1:3])),
	}
	return msg, nil
}
