package protocol
import (
	"encoding/binary"
	"bytes"
	"net"
	"fmt"
	"io"
)

type HelloMessage struct {
    CommandType uint8
    UdpPort     uint16
}

type SetStationMessage struct {
	CommandType uint8
	StationNumber uint16
}

type WelcomeMessage struct {
    ReplyType   uint8
    NumStations uint16
	SongName string
}

type AnnounceMessage struct {
	ReplyType uint8
	SongNameSize uint8
	SongName string
}

type InvalidCommandMessage struct {
	ReplyType uint8
	ReplyStringSize uint8
	ReplyString string
}

const (
	HelloMessageSize   = 3
	WelcomeMessageSize = 3
	SetStationMessageSize = 3
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

func DeserializeAnnounce(conn net.Conn) (*AnnounceMessage, error) {
	buffer := make([]byte, 1)
	_, err := conn.Read(buffer)
	if err != nil {
		return nil, err
	}
	songNameSize := buffer[0]
	buffer = make([]byte, songNameSize)
	_, err = io.ReadFull(conn, buffer)
	if err != nil {
		return nil, err
	}
	msg := &AnnounceMessage{
		ReplyType: 3,
		SongNameSize: songNameSize,
		SongName: string(buffer),
	}
	return msg, nil
}

func DeserializeInvalidCommand(conn net.Conn) (*InvalidCommandMessage, error) {
	buffer := make([]byte, 1)
	_, err := conn.Read(buffer)
	if err != nil {
		return nil, err
	}
	replyStringSize := buffer[0]
	buffer = make([]byte, replyStringSize)
	_, err = io.ReadFull(conn, buffer)
	if err != nil {
		return nil, err
	}
	msg := &InvalidCommandMessage{
		ReplyType: 4,
		ReplyStringSize: replyStringSize,
		ReplyString: string(buffer),
	}
	return msg, nil
}

func DeserializeServerMessage(conn net.Conn) (interface{}, error) {
	buffer := make([]byte, 1)
	_, err := conn.Read(buffer)
	if err != nil {
		return nil, err
	}
	replyType := buffer[0]
	switch replyType {
	case 2:
		return DeserializeWelcome(conn)
	case 3:
		return DeserializeAnnounce(conn)
	case 4:
		return DeserializeInvalidCommand(conn)
	default:
		return nil, fmt.Errorf("Unknown reply type: %d", replyType)
	}
}

func SerializeSetStation(m *SetStationMessage) ([]byte, error) {
	buffer := new(bytes.Buffer)
	err := binary.Write(buffer, binary.BigEndian, m.CommandType)
	if err != nil {
		return nil, err
	}
	err = binary.Write(buffer, binary.BigEndian, m.StationNumber)
	if err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func DeserializeSetStation(conn net.Conn) (*SetStationMessage, error) {
	buffer := make([]byte, SetStationMessageSize)
	_, err := conn.Read(buffer)
	if err != nil {
		return nil, err
	}

	msg := &SetStationMessage {
		CommandType: buffer[0], 
		StationNumber: uint16(binary.BigEndian.Uint16(buffer[1:])),
	}

	return msg, nil
}

func SerializeAnnounce(m *AnnounceMessage) ([]byte, error) {
	buffer := new(bytes.Buffer)
	err := binary.Write(buffer, binary.BigEndian, m.ReplyType)
	if err != nil {
		return nil, err
	}
	err = binary.Write(buffer, binary.BigEndian, m.SongNameSize)
	if err != nil {
		return nil, err
	}
	err = binary.Write(buffer, binary.BigEndian, m.SongName)
	if err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}
