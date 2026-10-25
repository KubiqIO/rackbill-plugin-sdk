package plugin

import (
	"context"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	"rackbill.com/sdk/plugin/proto"
)

// BackupPluginName is the key backup providers are served and dispensed under.
const BackupPluginName = "backup"

// Backup operation statuses. Long-running work returns Pending with a job ref
// and settles through GetJob.
const (
	BackupSucceeded = "succeeded"
	BackupPending   = "pending"
	BackupFailed    = "failed"
)

// Snapshot labels the agent sets. Plugins may report their own.
const (
	SnapshotScheduled = "scheduled"
	SnapshotManual    = "manual"
)

type BackupProviderInfo struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Version     string `json:"version"`
	Maturity    string `json:"maturity"` // Maturity* level (maturity.go)
	// Config is per repository, not per tenant: one operator may run several
	// unrelated backup locations with different credentials.
	ConfigFields []ConfigField `json:"config_fields"`

	SupportsQuota        bool `json:"supports_quota"`
	SupportsDirectBackup bool `json:"supports_direct_backup"`
	SupportsRestore      bool `json:"supports_restore"`
	Deduplicates         bool `json:"deduplicates"`
}

// BackupTarget is one service's space inside a repository. The agent persists
// it and passes it back verbatim; only the minting plugin reads its contents.
type BackupTarget struct {
	TargetRef string            `json:"target_ref"`
	Namespace string            `json:"namespace"`
	Options   map[string]string `json:"options,omitempty"`
}

type PrepareTargetInput struct {
	TenantID       string
	ServiceID      string
	ExternalRef    string
	Config         map[string]string
	QuotaBytes     int64
	QuotaSnapshots int
}

type PrepareTargetResult struct {
	Status        string
	FailureReason string
	Target        BackupTarget
}

type ReleaseTargetInput struct {
	TenantID  string
	ServiceID string
	Config    map[string]string
	Target    BackupTarget
	// Purge deletes the data now; without it the plugin may keep the
	// namespace for its own retention window.
	Purge bool
}

// Snapshot is one restorable point in time. SizeBytes is the backend's
// accounting for this snapshot; on a deduplicating store the sum across
// snapshots exceeds the repository's actual usage, so billing always uses
// Usage(), never a sum of these.
type Snapshot struct {
	SnapshotID  string            `json:"snapshot_id"`
	CreatedUnix int64             `json:"created_unix"`
	SizeBytes   int64             `json:"size_bytes"`
	Label       string            `json:"label"`
	Protected   bool              `json:"protected"`
	Verified    bool              `json:"verified"`
	Details     map[string]string `json:"details,omitempty"`
}

type BackupUsage struct {
	UsedBytes     int64 `json:"used_bytes"`
	SnapshotCount int   `json:"snapshot_count"`
	CapacityBytes int64 `json:"capacity_bytes"`
	LogicalBytes  int64 `json:"logical_bytes"`
}

type BackupResult struct {
	Status        string
	FailureReason string
}

// BackupOperation is the result of long-running work. Pending must carry a
// JobRef the host can poll.
type BackupOperation struct {
	Status        string
	FailureReason string
	JobRef        string
	SnapshotID    string
	SizeBytes     int64
	ProgressPct   int
}

type CreateSnapshotInput struct {
	TenantID    string
	ServiceID   string
	ExternalRef string
	Config      map[string]string
	Target      BackupTarget
	Label       string
	Options     map[string]string
}

type RestoreSnapshotInput struct {
	TenantID    string
	ServiceID   string
	ExternalRef string
	Config      map[string]string
	Target      BackupTarget
	SnapshotID  string
	Options     map[string]string
}

// BackupProvider is what a backup repository plugin implements. The agent
// owns quota policy, schedules and billing; the plugin owns the bytes.
//
// CreateSnapshot and RestoreSnapshot are optional (declare
// SupportsDirectBackup / SupportsRestore); providers that only serve as a
// destination for a provisioner's own backup tooling return an unimplemented
// error and the host never calls them.
type BackupProvider interface {
	Describe(ctx context.Context) (BackupProviderInfo, error)
	PrepareTarget(ctx context.Context, in PrepareTargetInput) (PrepareTargetResult, error)
	ReleaseTarget(ctx context.Context, in ReleaseTargetInput) (BackupResult, error)
	ListSnapshots(ctx context.Context, tenantID string, config map[string]string, target BackupTarget) ([]Snapshot, error)
	DeleteSnapshot(ctx context.Context, tenantID string, config map[string]string, target BackupTarget, snapshotID string) (BackupResult, error)
	Usage(ctx context.Context, tenantID string, config map[string]string, target BackupTarget) (BackupUsage, error)
	SetQuota(ctx context.Context, tenantID string, config map[string]string, target BackupTarget, quotaBytes int64, quotaSnapshots int) (BackupResult, error)
	CreateSnapshot(ctx context.Context, in CreateSnapshotInput) (BackupOperation, error)
	RestoreSnapshot(ctx context.Context, in RestoreSnapshotInput) (BackupOperation, error)
	GetJob(ctx context.Context, tenantID string, config map[string]string, jobRef string) (BackupOperation, error)
}

// BackupPlugin adapts a BackupProvider across the go-plugin gRPC boundary.
type BackupPlugin struct {
	goplugin.NetRPCUnsupportedPlugin
	Impl BackupProvider
}

func (p *BackupPlugin) GRPCServer(broker *goplugin.GRPCBroker, s *grpc.Server) error {
	proto.RegisterBackupProviderServer(s, &backupGRPCServer{impl: p.Impl})
	return nil
}

func (p *BackupPlugin) GRPCClient(ctx context.Context, broker *goplugin.GRPCBroker, c *grpc.ClientConn) (any, error) {
	return &backupGRPCClient{client: proto.NewBackupProviderClient(c)}, nil
}

// ServeBackupProvider is the entry point for backup plugin binaries.
func ServeBackupProvider(impl BackupProvider) { ServeBundle(Bundle{BackupProvider: impl}) }

// --- conversions ---

func targetToProto(t BackupTarget) *proto.BackupTarget {
	return &proto.BackupTarget{TargetRef: t.TargetRef, Namespace: t.Namespace, Options: t.Options}
}

func targetFromProto(t *proto.BackupTarget) BackupTarget {
	if t == nil {
		return BackupTarget{}
	}
	return BackupTarget{TargetRef: t.TargetRef, Namespace: t.Namespace, Options: t.Options}
}

func opToProto(op BackupOperation) *proto.BackupOperationResponse {
	return &proto.BackupOperationResponse{
		Status: op.Status, FailureReason: op.FailureReason, JobRef: op.JobRef,
		SnapshotId: op.SnapshotID, SizeBytes: op.SizeBytes, ProgressPct: int32(op.ProgressPct),
	}
}

func opFromProto(resp *proto.BackupOperationResponse) BackupOperation {
	if resp == nil {
		return BackupOperation{}
	}
	return BackupOperation{
		Status: resp.Status, FailureReason: resp.FailureReason, JobRef: resp.JobRef,
		SnapshotID: resp.SnapshotId, SizeBytes: resp.SizeBytes, ProgressPct: int(resp.ProgressPct),
	}
}

func snapshotsToProto(in []Snapshot) []*proto.Snapshot {
	out := make([]*proto.Snapshot, len(in))
	for i, s := range in {
		out[i] = &proto.Snapshot{
			SnapshotId: s.SnapshotID, CreatedUnix: s.CreatedUnix, SizeBytes: s.SizeBytes,
			Label: s.Label, Protected: s.Protected, Verified: s.Verified, Details: s.Details,
		}
	}
	return out
}

func snapshotsFromProto(in []*proto.Snapshot) []Snapshot {
	out := make([]Snapshot, len(in))
	for i, s := range in {
		out[i] = Snapshot{
			SnapshotID: s.SnapshotId, CreatedUnix: s.CreatedUnix, SizeBytes: s.SizeBytes,
			Label: s.Label, Protected: s.Protected, Verified: s.Verified, Details: s.Details,
		}
	}
	return out
}

// --- gRPC server adapter ---

type backupGRPCServer struct {
	proto.UnimplementedBackupProviderServer
	impl BackupProvider
}

func (s *backupGRPCServer) DescribeBackupProvider(ctx context.Context, _ *proto.DescribeBackupProviderRequest) (*proto.DescribeBackupProviderResponse, error) {
	info, err := s.impl.Describe(ctx)
	if err != nil {
		return nil, err
	}
	fields := configFieldsToProto(info.ConfigFields)
	return &proto.DescribeBackupProviderResponse{
		Name: info.Name, DisplayName: info.DisplayName, Version: info.Version, Maturity: info.Maturity,
		ConfigFields:         fields,
		SupportsQuota:        info.SupportsQuota,
		SupportsDirectBackup: info.SupportsDirectBackup,
		SupportsRestore:      info.SupportsRestore,
		Deduplicates:         info.Deduplicates,
	}, nil
}

func (s *backupGRPCServer) PrepareTarget(ctx context.Context, req *proto.PrepareTargetRequest) (*proto.PrepareTargetResponse, error) {
	res, err := s.impl.PrepareTarget(ctx, PrepareTargetInput{
		TenantID: req.TenantId, ServiceID: req.ServiceId, ExternalRef: req.ExternalRef,
		Config: req.Config, QuotaBytes: req.QuotaBytes, QuotaSnapshots: int(req.QuotaSnapshots),
	})
	if err != nil {
		return nil, err
	}
	return &proto.PrepareTargetResponse{
		Status: res.Status, FailureReason: res.FailureReason, Target: targetToProto(res.Target),
	}, nil
}

func (s *backupGRPCServer) ReleaseTarget(ctx context.Context, req *proto.ReleaseTargetRequest) (*proto.BackupActionResponse, error) {
	res, err := s.impl.ReleaseTarget(ctx, ReleaseTargetInput{
		TenantID: req.TenantId, ServiceID: req.ServiceId, Config: req.Config,
		Target: targetFromProto(req.Target), Purge: req.Purge,
	})
	if err != nil {
		return nil, err
	}
	return &proto.BackupActionResponse{Status: res.Status, FailureReason: res.FailureReason}, nil
}

func (s *backupGRPCServer) ListSnapshots(ctx context.Context, req *proto.ListSnapshotsRequest) (*proto.ListSnapshotsResponse, error) {
	snaps, err := s.impl.ListSnapshots(ctx, req.TenantId, req.Config, targetFromProto(req.Target))
	if err != nil {
		return nil, err
	}
	return &proto.ListSnapshotsResponse{Snapshots: snapshotsToProto(snaps)}, nil
}

func (s *backupGRPCServer) DeleteSnapshot(ctx context.Context, req *proto.DeleteSnapshotRequest) (*proto.BackupActionResponse, error) {
	res, err := s.impl.DeleteSnapshot(ctx, req.TenantId, req.Config, targetFromProto(req.Target), req.SnapshotId)
	if err != nil {
		return nil, err
	}
	return &proto.BackupActionResponse{Status: res.Status, FailureReason: res.FailureReason}, nil
}

func (s *backupGRPCServer) GetUsage(ctx context.Context, req *proto.GetUsageRequest) (*proto.GetUsageResponse, error) {
	u, err := s.impl.Usage(ctx, req.TenantId, req.Config, targetFromProto(req.Target))
	if err != nil {
		return nil, err
	}
	return &proto.GetUsageResponse{
		UsedBytes: u.UsedBytes, SnapshotCount: int32(u.SnapshotCount),
		CapacityBytes: u.CapacityBytes, LogicalBytes: u.LogicalBytes,
	}, nil
}

func (s *backupGRPCServer) SetQuota(ctx context.Context, req *proto.SetQuotaRequest) (*proto.BackupActionResponse, error) {
	res, err := s.impl.SetQuota(ctx, req.TenantId, req.Config, targetFromProto(req.Target),
		req.QuotaBytes, int(req.QuotaSnapshots))
	if err != nil {
		return nil, err
	}
	return &proto.BackupActionResponse{Status: res.Status, FailureReason: res.FailureReason}, nil
}

func (s *backupGRPCServer) CreateSnapshot(ctx context.Context, req *proto.CreateSnapshotRequest) (*proto.BackupOperationResponse, error) {
	op, err := s.impl.CreateSnapshot(ctx, CreateSnapshotInput{
		TenantID: req.TenantId, ServiceID: req.ServiceId, ExternalRef: req.ExternalRef,
		Config: req.Config, Target: targetFromProto(req.Target), Label: req.Label, Options: req.Options,
	})
	if err != nil {
		return nil, err
	}
	return opToProto(op), nil
}

func (s *backupGRPCServer) RestoreSnapshot(ctx context.Context, req *proto.RestoreSnapshotRequest) (*proto.BackupOperationResponse, error) {
	op, err := s.impl.RestoreSnapshot(ctx, RestoreSnapshotInput{
		TenantID: req.TenantId, ServiceID: req.ServiceId, ExternalRef: req.ExternalRef,
		Config: req.Config, Target: targetFromProto(req.Target), SnapshotID: req.SnapshotId,
		Options: req.Options,
	})
	if err != nil {
		return nil, err
	}
	return opToProto(op), nil
}

func (s *backupGRPCServer) GetBackupJob(ctx context.Context, req *proto.GetBackupJobRequest) (*proto.BackupOperationResponse, error) {
	op, err := s.impl.GetJob(ctx, req.TenantId, req.Config, req.JobRef)
	if err != nil {
		return nil, err
	}
	return opToProto(op), nil
}

// --- gRPC client adapter ---

type backupGRPCClient struct {
	client proto.BackupProviderClient
}

func (c *backupGRPCClient) Describe(ctx context.Context) (BackupProviderInfo, error) {
	resp, err := c.client.DescribeBackupProvider(ctx, &proto.DescribeBackupProviderRequest{})
	if err != nil {
		return BackupProviderInfo{}, err
	}
	fields := configFieldsFromProto(resp.ConfigFields)
	return BackupProviderInfo{
		Name: resp.Name, DisplayName: resp.DisplayName, Version: resp.Version, Maturity: resp.Maturity,
		ConfigFields:         fields,
		SupportsQuota:        resp.SupportsQuota,
		SupportsDirectBackup: resp.SupportsDirectBackup,
		SupportsRestore:      resp.SupportsRestore,
		Deduplicates:         resp.Deduplicates,
	}, nil
}

func (c *backupGRPCClient) PrepareTarget(ctx context.Context, in PrepareTargetInput) (PrepareTargetResult, error) {
	resp, err := c.client.PrepareTarget(ctx, &proto.PrepareTargetRequest{
		TenantId: in.TenantID, ServiceId: in.ServiceID, ExternalRef: in.ExternalRef,
		Config: in.Config, QuotaBytes: in.QuotaBytes, QuotaSnapshots: int32(in.QuotaSnapshots),
	})
	if err != nil {
		return PrepareTargetResult{}, err
	}
	return PrepareTargetResult{
		Status: resp.Status, FailureReason: resp.FailureReason, Target: targetFromProto(resp.Target),
	}, nil
}

func (c *backupGRPCClient) ReleaseTarget(ctx context.Context, in ReleaseTargetInput) (BackupResult, error) {
	resp, err := c.client.ReleaseTarget(ctx, &proto.ReleaseTargetRequest{
		TenantId: in.TenantID, ServiceId: in.ServiceID, Config: in.Config,
		Target: targetToProto(in.Target), Purge: in.Purge,
	})
	if err != nil {
		return BackupResult{}, err
	}
	return BackupResult{Status: resp.Status, FailureReason: resp.FailureReason}, nil
}

func (c *backupGRPCClient) ListSnapshots(ctx context.Context, tenantID string, config map[string]string, target BackupTarget) ([]Snapshot, error) {
	resp, err := c.client.ListSnapshots(ctx, &proto.ListSnapshotsRequest{
		TenantId: tenantID, Config: config, Target: targetToProto(target),
	})
	if err != nil {
		return nil, err
	}
	return snapshotsFromProto(resp.Snapshots), nil
}

func (c *backupGRPCClient) DeleteSnapshot(ctx context.Context, tenantID string, config map[string]string, target BackupTarget, snapshotID string) (BackupResult, error) {
	resp, err := c.client.DeleteSnapshot(ctx, &proto.DeleteSnapshotRequest{
		TenantId: tenantID, Config: config, Target: targetToProto(target), SnapshotId: snapshotID,
	})
	if err != nil {
		return BackupResult{}, err
	}
	return BackupResult{Status: resp.Status, FailureReason: resp.FailureReason}, nil
}

func (c *backupGRPCClient) Usage(ctx context.Context, tenantID string, config map[string]string, target BackupTarget) (BackupUsage, error) {
	resp, err := c.client.GetUsage(ctx, &proto.GetUsageRequest{
		TenantId: tenantID, Config: config, Target: targetToProto(target),
	})
	if err != nil {
		return BackupUsage{}, err
	}
	return BackupUsage{
		UsedBytes: resp.UsedBytes, SnapshotCount: int(resp.SnapshotCount),
		CapacityBytes: resp.CapacityBytes, LogicalBytes: resp.LogicalBytes,
	}, nil
}

func (c *backupGRPCClient) SetQuota(ctx context.Context, tenantID string, config map[string]string, target BackupTarget, quotaBytes int64, quotaSnapshots int) (BackupResult, error) {
	resp, err := c.client.SetQuota(ctx, &proto.SetQuotaRequest{
		TenantId: tenantID, Config: config, Target: targetToProto(target),
		QuotaBytes: quotaBytes, QuotaSnapshots: int32(quotaSnapshots),
	})
	if err != nil {
		return BackupResult{}, err
	}
	return BackupResult{Status: resp.Status, FailureReason: resp.FailureReason}, nil
}

func (c *backupGRPCClient) CreateSnapshot(ctx context.Context, in CreateSnapshotInput) (BackupOperation, error) {
	resp, err := c.client.CreateSnapshot(ctx, &proto.CreateSnapshotRequest{
		TenantId: in.TenantID, ServiceId: in.ServiceID, ExternalRef: in.ExternalRef,
		Config: in.Config, Target: targetToProto(in.Target), Label: in.Label, Options: in.Options,
	})
	if err != nil {
		return BackupOperation{}, err
	}
	return opFromProto(resp), nil
}

func (c *backupGRPCClient) RestoreSnapshot(ctx context.Context, in RestoreSnapshotInput) (BackupOperation, error) {
	resp, err := c.client.RestoreSnapshot(ctx, &proto.RestoreSnapshotRequest{
		TenantId: in.TenantID, ServiceId: in.ServiceID, ExternalRef: in.ExternalRef,
		Config: in.Config, Target: targetToProto(in.Target), SnapshotId: in.SnapshotID,
		Options: in.Options,
	})
	if err != nil {
		return BackupOperation{}, err
	}
	return opFromProto(resp), nil
}

func (c *backupGRPCClient) GetJob(ctx context.Context, tenantID string, config map[string]string, jobRef string) (BackupOperation, error) {
	resp, err := c.client.GetBackupJob(ctx, &proto.GetBackupJobRequest{
		TenantId: tenantID, Config: config, JobRef: jobRef,
	})
	if err != nil {
		return BackupOperation{}, err
	}
	return opFromProto(resp), nil
}
