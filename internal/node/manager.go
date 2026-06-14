package node

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"snowcast-jamesyan2028/internal/leadership"
	"snowcast-jamesyan2028/internal/replication"
	"snowcast-jamesyan2028/internal/runtime"
	"snowcast-jamesyan2028/internal/state"
	"snowcast-jamesyan2028/internal/control"
	"snowcast-jamesyan2028/pkg/wal"

	clientv3 "go.etcd.io/etcd/client/v3"
)

// Config holds node role and networking settings.
type Config struct {
	EtcdEndpoints string
	EtcdKey       string
	LeaseTTL      int64
	LeasePoll     time.Duration
	ClientPort    string
	ReplPort      string
	BackupAddr    string
	WalPath       string
	Files         []string
}

// Manager orchestrates leader/standby role transitions.
type Manager struct {
	cfg Config

	etcdClient *clientv3.Client
	wal        *wal.Log

	mu         sync.Mutex
	role       string // "leader" or "standby"
	replServer *replication.ReplServer
	clientSrv  *runtime.Server
	session    *leadership.Session
	keepCancel context.CancelFunc
}

const (
	roleLeader  = "leader"
	roleStandby = "standby"
)

// New opens WAL and etcd client.
func New(cfg Config) (*Manager, error) {
	if cfg.EtcdKey == "" {
		cfg.EtcdKey = leadership.DefaultKey
	}
	if cfg.LeasePoll == 0 {
		cfg.LeasePoll = time.Second
	}
	if cfg.LeaseTTL == 0 {
		cfg.LeaseTTL = 5
	}

	w, err := wal.Open(cfg.WalPath)
	if err != nil {
		return nil, err
	}

	ec, err := leadership.NewClient(cfg.EtcdEndpoints)
	if err != nil {
		w.Close()
		return nil, err
	}

	return &Manager{cfg: cfg, wal: w, etcdClient: ec, role: roleStandby}, nil
}

// Close releases etcd and WAL.
func (m *Manager) Close() {
	m.stopKeepalive()
	if m.clientSrv != nil {
		m.clientSrv.Stop()
	}
	if m.replServer != nil {
		m.replServer.GracefulStop()
	}
	replication.ShutdownGlobal()
	if m.wal != nil {
		m.wal.Close()
	}
	if m.etcdClient != nil {
		m.etcdClient.Close()
	}
}

// RunAsLeader acquires etcd lease and serves clients.
func (m *Manager) RunAsLeader() error {
	state.InitStations(m.cfg.Files)

	ctx := context.Background()
	primaryValue := fmt.Sprintf("127.0.0.1:%s", m.cfg.ClientPort)
	session, err := leadership.Acquire(ctx, m.etcdClient, m.cfg.EtcdKey, primaryValue, m.cfg.LeaseTTL)
	if err != nil {
		return err
	}

	m.mu.Lock()
	m.session = session
	m.role = roleLeader
	m.mu.Unlock()

	if err := replication.StartWithWal(m.wal, m.cfg.BackupAddr); err != nil {
		return err
	}

	clientSrv, err := runtime.Serve(m.cfg.ClientPort, m.cfg.Files, replication.Global())
	if err != nil {
		replication.ShutdownGlobal()
		return err
	}
	m.clientSrv = clientSrv

	m.startKeepalive(func() {
		log.Printf("Lost etcd lease; demoting to standby")
		m.demoteToStandby()
	})

	runtime.HandleUserInput(clientSrv)
	return nil
}

// RunAsStandby starts inbound replication and polls etcd for promotion.
func (m *Manager) RunAsStandby() error {
	state.InitStations(m.cfg.Files)

	if err := m.startReplServerWithRetry(); err != nil {
		return err
	}

	log.Printf("Standby mode: replication on :%s, polling etcd", m.cfg.ReplPort)
	m.runEtcdPollLoop()
	return nil
}

func (m *Manager) runEtcdPollLoop() {
	seenPrimary := false
	for {
		time.Sleep(m.cfg.LeasePoll)

		m.mu.Lock()
		role := m.role
		m.mu.Unlock()
		if role == roleLeader {
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, held, err := leadership.GetPrimary(ctx, m.etcdClient, m.cfg.EtcdKey)
		cancel()
		if err != nil {
			log.Printf("etcd get primary: %v", err)
			continue
		}
		if held {
			seenPrimary = true
			continue
		}
		if !seenPrimary {
			continue
		}

		promoteCtx, promoteCancel := context.WithTimeout(context.Background(), 5*time.Second)
		primaryValue := fmt.Sprintf("127.0.0.1:%s", m.cfg.ClientPort)
		session, ok, err := leadership.TryAcquire(promoteCtx, m.etcdClient, m.cfg.EtcdKey, primaryValue, m.cfg.LeaseTTL)
		promoteCancel()
		if err != nil {
			log.Printf("etcd try acquire: %v", err)
			continue
		}
		if !ok {
			continue
		}

		if err := m.promoteToLeader(session); err != nil {
			log.Printf("promotion failed: %v", err)
			_ = session.Revoke(context.Background())
			continue
		}
		return
	}
}

func (m *Manager) promoteToLeader(session *leadership.Session) error {
	m.mu.Lock()
	if m.role == roleLeader {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()

	log.Printf("Promoting to leader on port %s", m.cfg.ClientPort)

	m.mu.Lock()
	m.session = session
	m.role = roleLeader
	m.mu.Unlock()
	m.startKeepalive(func() {
		log.Printf("Lost etcd lease; demoting to standby")
		m.demoteToStandby()
	})

	if m.replServer != nil {
		m.replServer.GracefulStop()
		m.replServer = nil
	}

	entries, err := m.wal.ReadAll()
	if err != nil {
		return err
	}
	state.InitStations(m.cfg.Files)
	for _, entry := range entries {
		if err := state.ApplyWalEntry(entry); err != nil {
			return fmt.Errorf("apply wal seq %d: %w", entry.Seq, err)
		}
	}

	replTarget := m.cfg.BackupAddr
	if replTarget != "" {
		if err := replication.StartWithWalTimeout(m.wal, replTarget, 2*time.Second); err != nil {
			log.Printf("outbound replication unavailable (%v); serving with local WAL only", err)
			replTarget = ""
		}
	}

	var repl control.Replicator
	if replTarget == "" {
		repl = replication.NewLocalFromLog(m.wal)
	} else {
		repl = replication.Global()
	}

	clientSrv, err := runtime.Serve(m.cfg.ClientPort, m.cfg.Files, repl)
	if err != nil {
		replication.ShutdownGlobal()
		return err
	}

	m.mu.Lock()
	m.clientSrv = clientSrv
	m.mu.Unlock()

	log.Printf("Promoted to leader on port %s", m.cfg.ClientPort)
	select {}
}

func (m *Manager) demoteToStandby() {
	m.mu.Lock()
	if m.role == roleStandby {
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()

	m.stopKeepalive()

	if m.clientSrv != nil {
		m.clientSrv.Stop()
		m.clientSrv = nil
	}
	replication.ShutdownGlobal()

	m.mu.Lock()
	m.session = nil
	m.role = roleStandby
	m.mu.Unlock()

	if err := m.startReplServerWithRetry(); err != nil {
		log.Fatalf("failed to start replication server after demotion: %v", err)
	}

	log.Printf("Demoted to standby; replication on :%s", m.cfg.ReplPort)
	m.runEtcdPollLoop()
}

func (m *Manager) startReplServerWithRetry() error {
	deadline := time.Now().Add(30 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		rs, err := replication.StartReplServer(m.cfg.ReplPort, m.wal)
		if err == nil {
			m.replServer = rs
			return nil
		}
		lastErr = err
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("start repl server on :%s: %w", m.cfg.ReplPort, lastErr)
}

func (m *Manager) startKeepalive(onLost func()) {
	m.stopKeepalive()
	ctx, cancel := context.WithCancel(context.Background())
	m.keepCancel = cancel

	m.mu.Lock()
	session := m.session
	m.mu.Unlock()
	if session == nil {
		return
	}
	session.SetOnLost(onLost)
	go func() {
		if err := session.KeepAlive(ctx); err != nil && ctx.Err() == nil {
			log.Printf("keepalive ended: %v", err)
		}
	}()
}

func (m *Manager) stopKeepalive() {
	if m.keepCancel != nil {
		m.keepCancel()
		m.keepCancel = nil
	}
}
