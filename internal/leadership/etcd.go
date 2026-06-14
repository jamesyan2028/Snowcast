package leadership

import (
	"context"
	"fmt"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

const DefaultKey = "/snowcast/primary"

// Session holds an etcd lease tied to the primary registration key.
type Session struct {
	client  *clientv3.Client
	leaseID clientv3.LeaseID
	key     string
	value   string
	onLost  func()
}

// NewClient connects to etcd.
func NewClient(endpoints string) (*clientv3.Client, error) {
	return clientv3.New(clientv3.Config{
		Endpoints:   []string{endpoints},
		DialTimeout: 5 * time.Second,
	})
}

// Acquire grants a lease and creates key if absent.
func Acquire(ctx context.Context, client *clientv3.Client, key, value string, ttl int64) (*Session, error) {
	s, ok, err := TryAcquire(ctx, client, key, value, ttl)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("leadership key %q already held", key)
	}
	return s, nil
}

// TryAcquire attempts to become primary; returns ok=false if key exists.
func TryAcquire(ctx context.Context, client *clientv3.Client, key, value string, ttl int64) (*Session, bool, error) {
	lease, err := client.Grant(ctx, ttl)
	if err != nil {
		return nil, false, err
	}

	txn := client.Txn(ctx).
		If(clientv3.Compare(clientv3.CreateRevision(key), "=", 0)).
		Then(clientv3.OpPut(key, value, clientv3.WithLease(lease.ID)))

	resp, err := txn.Commit()
	if err != nil {
		_, _ = client.Revoke(context.Background(), lease.ID)
		return nil, false, err
	}
	if !resp.Succeeded {
		_, _ = client.Revoke(context.Background(), lease.ID)
		return nil, false, nil
	}

	return &Session{
		client:  client,
		leaseID: lease.ID,
		key:     key,
		value:   value,
	}, true, nil
}

// GetPrimary returns the registered primary address if the key exists.
func GetPrimary(ctx context.Context, client *clientv3.Client, key string) (value string, held bool, err error) {
	resp, err := client.Get(ctx, key)
	if err != nil {
		return "", false, err
	}
	if len(resp.Kvs) == 0 {
		return "", false, nil
	}
	return string(resp.Kvs[0].Value), true, nil
}

// SetOnLost registers a callback when keepalive fails.
func (s *Session) SetOnLost(fn func()) {
	s.onLost = fn
}

// KeepAlive blocks until the lease is lost or ctx is cancelled.
func (s *Session) KeepAlive(ctx context.Context) error {
	ch, err := s.client.KeepAlive(ctx, s.leaseID)
	if err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ka, ok := <-ch:
			if !ok {
				if s.onLost != nil {
					s.onLost()
				}
				return fmt.Errorf("etcd lease keepalive lost")
			}
			if ka == nil {
				if s.onLost != nil {
					s.onLost()
				}
				return fmt.Errorf("etcd lease revoked")
			}
		}
	}
}

// Revoke releases the lease and deletes the key.
func (s *Session) Revoke(ctx context.Context) error {
	_, err := s.client.Revoke(ctx, s.leaseID)
	return err
}

// Close closes the etcd client.
func (s *Session) Close() error {
	return s.client.Close()
}
