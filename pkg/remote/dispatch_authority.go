package remote

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/seyi/dagens/pkg/observability"
	clientv3 "go.etcd.io/etcd/client/v3"
)

const (
	dispatchAuthorityLeaderIDKey = "dispatch_leader_id"
	dispatchAuthorityEpochKey    = "dispatch_epoch"
	dispatchAuthorityScopeKey    = "dispatch_scope"
)

var (
	ErrMissingDispatchAuthority = errors.New("missing dispatch authority")
	ErrStaleDispatchAuthority   = errors.New("stale dispatch authority")
)

type dispatchAuthorityContextKey struct{}

// DispatchAuthority describes the scheduler authority stamped onto a remote
// dispatch request so workers can reject stale leader traffic.
type DispatchAuthority struct {
	LeaderID string
	Epoch    string
	Scope    string
}

// WithDispatchAuthority attaches scheduler authority to a dispatch context.
func WithDispatchAuthority(ctx context.Context, authority DispatchAuthority) context.Context {
	return context.WithValue(ctx, dispatchAuthorityContextKey{}, authority)
}

func dispatchAuthorityFromContext(ctx context.Context) (DispatchAuthority, bool) {
	if ctx == nil {
		return DispatchAuthority{}, false
	}
	authority, ok := ctx.Value(dispatchAuthorityContextKey{}).(DispatchAuthority)
	if !ok {
		return DispatchAuthority{}, false
	}
	return authority, true
}

func (a DispatchAuthority) metadata() map[string]interface{} {
	metadata := map[string]interface{}{}
	if strings.TrimSpace(a.LeaderID) != "" {
		metadata[dispatchAuthorityLeaderIDKey] = strings.TrimSpace(a.LeaderID)
	}
	if strings.TrimSpace(a.Epoch) != "" {
		metadata[dispatchAuthorityEpochKey] = strings.TrimSpace(a.Epoch)
	}
	if strings.TrimSpace(a.Scope) != "" {
		metadata[dispatchAuthorityScopeKey] = strings.TrimSpace(a.Scope)
	}
	return metadata
}

func authorityFromMetadata(metadata map[string]interface{}) DispatchAuthority {
	if len(metadata) == 0 {
		return DispatchAuthority{}
	}
	return DispatchAuthority{
		LeaderID: metadataString(metadata, dispatchAuthorityLeaderIDKey),
		Epoch:    metadataString(metadata, dispatchAuthorityEpochKey),
		Scope:    metadataString(metadata, dispatchAuthorityScopeKey),
	}
}

func metadataString(metadata map[string]interface{}, key string) string {
	value, ok := metadata[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf("%v", value))
}

func authorityRejectionReason(err error) string {
	if err == nil {
		return "unknown"
	}
	lowered := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, ErrMissingDispatchAuthority):
		return "missing_authority"
	case errors.Is(err, ErrStaleDispatchAuthority):
		return "stale_epoch"
	case strings.Contains(lowered, "timeout") || errors.Is(err, context.DeadlineExceeded):
		return "network_timeout"
	case strings.Contains(lowered, "connection refused") ||
		strings.Contains(lowered, "connection reset") ||
		strings.Contains(lowered, "unavailable"):
		return "transport_error"
	default:
		return "authority_unavailable"
	}
}

// AuthorityRejectionReason exposes the canonical reason mapping for dispatch
// authority validation failures so scheduler and worker metrics stay aligned.
func AuthorityRejectionReason(err error) string {
	return authorityRejectionReason(err)
}

// DispatchAuthorityValidator validates remote execution requests against the
// current control-plane authority before local execution begins.
type DispatchAuthorityValidator interface {
	Validate(ctx context.Context, authority DispatchAuthority) error
}

// EtcdDispatchAuthorityValidator validates scheduler authority using the
// control-plane leadership key in etcd.
type EtcdDispatchAuthorityValidator struct {
	client       *clientv3.Client
	electionKey  string
	queryTimeout time.Duration
}

// EtcdDispatchAuthorityValidatorConfig configures worker-side authority validation.
type EtcdDispatchAuthorityValidatorConfig struct {
	Endpoints    []string
	ElectionKey  string
	DialTimeout  time.Duration
	QueryTimeout time.Duration
}

func NewEtcdDispatchAuthorityValidator(cfg EtcdDispatchAuthorityValidatorConfig) (*EtcdDispatchAuthorityValidator, error) {
	if len(cfg.Endpoints) == 0 {
		return nil, fmt.Errorf("etcd authority validator endpoints are required")
	}
	if strings.TrimSpace(cfg.ElectionKey) == "" {
		cfg.ElectionKey = "/dagens/control-plane/scheduler"
	}
	if cfg.DialTimeout <= 0 {
		cfg.DialTimeout = 5 * time.Second
	}
	if cfg.QueryTimeout <= 0 {
		cfg.QueryTimeout = 2 * time.Second
	}

	client, err := clientv3.New(clientv3.Config{
		Endpoints:   cfg.Endpoints,
		DialTimeout: cfg.DialTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("create etcd authority validator client: %w", err)
	}

	return &EtcdDispatchAuthorityValidator{
		client:       client,
		electionKey:  cfg.ElectionKey,
		queryTimeout: cfg.QueryTimeout,
	}, nil
}

func (v *EtcdDispatchAuthorityValidator) Close() error {
	if v == nil || v.client == nil {
		return nil
	}
	return v.client.Close()
}

func (v *EtcdDispatchAuthorityValidator) Validate(ctx context.Context, authority DispatchAuthority) error {
	if strings.TrimSpace(authority.LeaderID) == "" || strings.TrimSpace(authority.Epoch) == "" {
		return fmt.Errorf("%w: leader_id=%q epoch=%q", ErrMissingDispatchAuthority, authority.LeaderID, authority.Epoch)
	}

	// Always bind the etcd query with the validator timeout.
	var cancel context.CancelFunc
	ctx, cancel = context.WithTimeout(ctx, v.queryTimeout)
	defer cancel()

	electionPrefix := v.electionKey
	if !strings.HasSuffix(electionPrefix, "/") {
		electionPrefix += "/"
	}

	resp, err := v.client.Get(ctx, electionPrefix, clientv3.WithPrefix(), clientv3.WithSort(clientv3.SortByCreateRevision, clientv3.SortAscend), clientv3.WithLimit(1))
	if err != nil {
		return fmt.Errorf("query current dispatch authority at %q: %w", v.electionKey, err)
	}

	current := DispatchAuthority{}
	if resp != nil && len(resp.Kvs) > 0 {
		current.LeaderID = string(resp.Kvs[0].Value)
		// Use the leader key's mod revision as the fencing epoch. This changes
		// exactly when leadership ownership changes, unlike the global header revision.
		current.Epoch = fmt.Sprintf("%d", resp.Kvs[0].ModRevision)
	}

	if current.LeaderID == "" || current.Epoch == "" {
		return fmt.Errorf("%w: current dispatch authority unavailable in etcd at %q", ErrMissingDispatchAuthority, v.electionKey)
	}
	if authority.LeaderID != current.LeaderID || authority.Epoch != current.Epoch {
		return fmt.Errorf("%w: request leader_id=%q epoch=%q current leader_id=%q epoch=%q", ErrStaleDispatchAuthority, authority.LeaderID, authority.Epoch, current.LeaderID, current.Epoch)
	}
	return nil
}

func recordAuthorityValidationFailure(metrics *observability.Metrics, err error) {
	if metrics == nil {
		return
	}
	metrics.RecordWorkerDispatchRejection(authorityRejectionReason(err))
}
