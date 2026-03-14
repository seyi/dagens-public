package scheduler

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/seyi/dagens/pkg/coordination"
	clientv3 "go.etcd.io/etcd/client/v3"
)

// EtcdLeadershipProviderConfig configures etcd-backed leadership checks.
type EtcdLeadershipProviderConfig struct {
	Endpoints     []string
	ElectionKey   string
	Identity      string
	SessionTTL    int
	ResignTimeout time.Duration
	DialTimeout   time.Duration
}

// EtcdLeadershipProvider campaigns for leadership and reports dispatch authority.
type EtcdLeadershipProvider struct {
	client      *clientv3.Client
	elector     *coordination.LeaderElector
	electionKey string
	identity    string
	started     atomic.Bool
	stopOnce    sync.Once
}

// NewEtcdLeadershipProvider constructs an etcd-backed leadership provider.
func NewEtcdLeadershipProvider(cfg EtcdLeadershipProviderConfig) (*EtcdLeadershipProvider, error) {
	if len(cfg.Endpoints) == 0 {
		return nil, fmt.Errorf("etcd leadership endpoints are required")
	}
	if strings.TrimSpace(cfg.Identity) == "" {
		return nil, fmt.Errorf("etcd leadership identity is required")
	}
	if strings.TrimSpace(cfg.ElectionKey) == "" {
		cfg.ElectionKey = "/dagens/control-plane/scheduler"
	}
	if cfg.DialTimeout <= 0 {
		cfg.DialTimeout = 5 * time.Second
	}
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = 10
	}

	client, err := clientv3.New(clientv3.Config{
		Endpoints:   cfg.Endpoints,
		DialTimeout: cfg.DialTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create etcd leadership client: %w", err)
	}

	elector, err := coordination.NewLeaderElector(coordination.LeaderElectionConfig{
		Client:        client,
		Key:           cfg.ElectionKey,
		Identity:      cfg.Identity,
		SessionTTL:    cfg.SessionTTL,
		ResignTimeout: cfg.ResignTimeout,
	})
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("failed to create etcd leader elector: %w", err)
	}

	return &EtcdLeadershipProvider{
		client:      client,
		elector:     elector,
		electionKey: cfg.ElectionKey,
		identity:    cfg.Identity,
	}, nil
}

// Start begins etcd campaign/observe loops.
func (p *EtcdLeadershipProvider) Start(ctx context.Context) error {
	if p.started.Load() {
		return nil
	}
	if err := p.elector.Start(ctx); err != nil {
		return err
	}
	p.started.Store(true)
	return nil
}

// Stop stops election and closes etcd client resources.
func (p *EtcdLeadershipProvider) Stop() {
	p.stopOnce.Do(func() {
		p.started.Store(false)
		p.elector.Stop()
		_ = p.client.Close()
	})
}

// DispatchAuthority returns current leader status and fencing token.
func (p *EtcdLeadershipProvider) DispatchAuthority(ctx context.Context) (LeadershipAuthority, error) {
	if !p.started.Load() {
		return LeadershipAuthority{}, fmt.Errorf("etcd leadership provider not started")
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
	}

	leaderID := ""
	epoch := ""
	electionPrefix := p.electionKey
	if !strings.HasSuffix(electionPrefix, "/") {
		electionPrefix += "/"
	}

	resp, err := p.client.Get(ctx, electionPrefix, clientv3.WithPrefix(), clientv3.WithSort(clientv3.SortByCreateRevision, clientv3.SortAscend), clientv3.WithLimit(1))
	if err != nil {
		return LeadershipAuthority{}, fmt.Errorf("failed to query etcd leadership key: %w", err)
	}
	if resp != nil {
		epoch = fmt.Sprintf("%d", resp.Header.Revision)
		if len(resp.Kvs) > 0 {
			leaderID = string(resp.Kvs[0].Value)
			epoch = fmt.Sprintf("%d", resp.Kvs[0].ModRevision)
		}
	}
	localLeader := p.elector.IsLeader()
	if localLeader && leaderID != "" && leaderID != p.identity {
		log.Printf("leadership mismatch: elector reports leader but etcd key owner differs (identity=%s etcd_leader=%s epoch=%s key=%s)", p.identity, leaderID, epoch, p.electionKey)
	}

	return LeadershipAuthority{
		IsLeader: localLeader && leaderID == p.identity,
		Epoch:    epoch,
		LeaderID: leaderID,
	}, nil
}

// DispatchAuthorityForScope returns scope-annotated authority. In v0.2 this
// provider still campaigns on a single election key and does not create
// per-scope elections; the scope annotation is carried for audit/routing
// compatibility with ScopedLeadershipProvider consumers.
func (p *EtcdLeadershipProvider) DispatchAuthorityForScope(ctx context.Context, scope string) (LeadershipAuthority, error) {
	auth, err := p.DispatchAuthority(ctx)
	if err != nil {
		return LeadershipAuthority{}, err
	}
	auth.Scope = scope
	return auth, nil
}
