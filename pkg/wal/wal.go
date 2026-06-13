package wal

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sync"
	pb "snowcast-jamesyan2028/pkg/protocol"
	"github.com/golang/protobuf/proto"
)

type Log struct {
	path string
	file *os.File
	mu   sync.Mutex
	seq  uint64
}

// open or create a WAL at input path
func Open(path string) (*Log, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, err
	}
	l := &Log{path: path, file: f}

	// get most up to date sequence number from WAL
	if err := l.recoverSeq(); err != nil {
		f.Close()
		return nil, err
	}
	return l, nil
}

func (l *Log) recoverSeq() error {
	entries, err := l.readAllUnlocked()
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Seq > l.seq {
			l.seq = e.Seq
		}
	}
	return nil
}

// add a new WAL entry, keeping track of the sequnece number
func (l *Log) Append(entry *pb.WalEntry) (uint64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.seq++
	entry.Seq = l.seq

	data, err := proto.Marshal(entry)
	if err != nil {
		l.seq--
		return 0, err
	}

	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, uint32(len(data)))
	if _, err := l.file.Write(header); err != nil {
		l.seq--
		return 0, err
	}
	if _, err := l.file.Write(data); err != nil {
		l.seq--
		return 0, err
	}
	if err := l.file.Sync(); err != nil {
		l.seq--
		return 0, err
	}
	return entry.Seq, nil
}

// AppendReplicated writes an entry with a pre-assigned seq (backup replication path).
func (l *Log) AppendReplicated(entry *pb.WalEntry) error {
	if entry.Seq == 0 {
		return fmt.Errorf("wal: replicated entry missing seq")
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	data, err := proto.Marshal(entry)
	if err != nil {
		return err
	}

	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, uint32(len(data)))
	if _, err := l.file.Write(header); err != nil {
		return err
	}
	if _, err := l.file.Write(data); err != nil {
		return err
	}
	if err := l.file.Sync(); err != nil {
		return err
	}
	if entry.Seq > l.seq {
		l.seq = entry.Seq
	}
	return nil
}

// returns the highest sequence number written.
func (l *Log) LastSeq() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.seq
}

// returns all entries in order.
func (l *Log) ReadAll() ([]*pb.WalEntry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.readAllUnlocked()
}

func (l *Log) readAllUnlocked() ([]*pb.WalEntry, error) {
	if _, err := l.file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}

	var entries []*pb.WalEntry
	for {
		header := make([]byte, 4)
		_, err := io.ReadFull(l.file, header)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		size := binary.BigEndian.Uint32(header)
		if size == 0 {
			return nil, fmt.Errorf("wal: invalid record size 0")
		}
		data := make([]byte, size)
		if _, err := io.ReadFull(l.file, data); err != nil {
			return nil, err
		}
		entry := &pb.WalEntry{}
		if err := proto.Unmarshal(data, entry); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// closes the WAL file.
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}
