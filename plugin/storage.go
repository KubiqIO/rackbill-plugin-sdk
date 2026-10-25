package plugin

import (
	"context"
	"encoding/json"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"rackbill.com/sdk/plugin/proto"
)

const StoragePluginName = "storage"

// StorageSchema is a declarative, versioned schema. Names use [a-z][a-z0-9_]*
// (at most 48 characters). Tables always have host-owned tenant_id and key
// columns; neither may be declared here. Upgrades may add tables and optional
// columns. Removing/changing columns or downgrading a version is refused.
type StorageSchema struct {
	Version int64          `json:"version"`
	Tables  []StorageTable `json:"tables"`
}

type StorageTable struct {
	Name    string          `json:"name"`
	Columns []StorageColumn `json:"columns"`
}

type StorageColumn struct {
	Name     string `json:"name"`
	Type     string `json:"type"` // text, integer (int64), boolean, json
	Required bool   `json:"required,omitempty"`
}

// StorageProvider opts a plugin into durable storage. DescribeStorage must be
// pure: it runs before storage is connected. OpenStorage receives a client only
// after the host has committed the schema. It must retain that client safely for
// concurrent calls. A single-kind Serve* detects this interface automatically;
// bundles can set Bundle.Storage explicitly to share one schema across kinds.
type StorageProvider interface {
	DescribeStorage(context.Context) (StorageSchema, error)
	OpenStorage(context.Context, Storage) error
}

// Storage offers no SQL, credentials, or plugin selector. The host binds this
// handle to the primary kind and Describe name, shared by a bundle's kinds.
// Every call must name the tenant from its input; an empty tenant is refused.
// Put atomically replaces the whole row (omitted optional fields become null).
// Get returns codes.NotFound for an absent row; Delete is idempotent. List
// returns at most 25 rows, ordered by key; pass the last key as after to page.
// Values is a JSON object of declared columns, at most 64 KiB. Storage is for
// plugin state, not credentials: values are not sealed by RackBill.
type Storage interface {
	Put(ctx context.Context, tenant, table, key string, values json.RawMessage) error
	Get(ctx context.Context, tenant, table, key string) (json.RawMessage, error)
	Delete(ctx context.Context, tenant, table, key string) error
	List(ctx context.Context, tenant, table, after string, limit int) ([]StorageRecord, error)
}

type StorageRecord struct {
	Key    string          `json:"key"`
	Values json.RawMessage `json:"values"`
}

// StorageConnector is the host's view of the optional manifest.
type StorageConnector interface {
	DescribeStorage(context.Context) (StorageSchema, error)
	ConnectStorage(context.Context, Storage) error
}

type StoragePlugin struct {
	goplugin.NetRPCUnsupportedPlugin
	Impl StorageProvider
}

func (p *StoragePlugin) GRPCServer(b *goplugin.GRPCBroker, s *grpc.Server) error {
	proto.RegisterStorageManifestServer(s, &storageManifestServer{broker: b, impl: p.Impl})
	return nil
}

func (p *StoragePlugin) GRPCClient(_ context.Context, b *goplugin.GRPCBroker, c *grpc.ClientConn) (any, error) {
	return &storageConnector{broker: b, client: proto.NewStorageManifestClient(c)}, nil
}

type storageManifestServer struct {
	proto.UnimplementedStorageManifestServer
	broker *goplugin.GRPCBroker
	impl   StorageProvider
}

func (s *storageManifestServer) DescribeStorage(ctx context.Context, _ *proto.StorageDescribeRequest) (*proto.StorageDescribeResponse, error) {
	schema, err := s.impl.DescribeStorage(ctx)
	if err != nil {
		return nil, err
	}
	out := &proto.StorageDescribeResponse{Version: schema.Version}
	for _, t := range schema.Tables {
		pt := &proto.StorageTable{Name: t.Name}
		for _, c := range t.Columns {
			pt.Columns = append(pt.Columns, &proto.StorageColumn{Name: c.Name, Type: c.Type, Required: c.Required})
		}
		out.Tables = append(out.Tables, pt)
	}
	return out, nil
}

func (s *storageManifestServer) ConnectStorage(ctx context.Context, in *proto.StorageConnectRequest) (*proto.StorageEmpty, error) {
	conn, err := s.broker.Dial(in.BrokerId)
	if err != nil {
		return nil, err
	}
	if err := s.impl.OpenStorage(ctx, &storageClient{client: proto.NewStorageDataClient(conn)}); err != nil {
		_ = conn.Close()
		return nil, err
	}
	// The connection lasts for this plugin process; the host closes its broker
	// on unload or crash. OpenStorage's context only bounds initialization.
	return &proto.StorageEmpty{}, nil
}

type storageConnector struct {
	broker *goplugin.GRPCBroker
	client proto.StorageManifestClient
}

func (c *storageConnector) DescribeStorage(ctx context.Context) (StorageSchema, error) {
	in, err := c.client.DescribeStorage(ctx, &proto.StorageDescribeRequest{})
	if err != nil {
		return StorageSchema{}, err
	}
	out := StorageSchema{Version: in.Version}
	for _, t := range in.Tables {
		st := StorageTable{Name: t.Name}
		for _, col := range t.Columns {
			st.Columns = append(st.Columns, StorageColumn{Name: col.Name, Type: col.Type, Required: col.Required})
		}
		out.Tables = append(out.Tables, st)
	}
	return out, nil
}

func (c *storageConnector) ConnectStorage(ctx context.Context, storage Storage) error {
	id := c.broker.NextId()
	go c.broker.AcceptAndServe(id, func(opts []grpc.ServerOption) *grpc.Server {
		s := grpc.NewServer(opts...)
		proto.RegisterStorageDataServer(s, &storageDataServer{impl: storage})
		return s
	})
	_, err := c.client.ConnectStorage(ctx, &proto.StorageConnectRequest{BrokerId: id})
	return err
}

type storageDataServer struct {
	proto.UnimplementedStorageDataServer
	impl Storage
}

func (s *storageDataServer) Put(ctx context.Context, in *proto.StoragePutRequest) (*proto.StorageEmpty, error) {
	err := s.impl.Put(ctx, in.TenantId, in.Table, in.Key, in.Values)
	return &proto.StorageEmpty{}, err
}
func (s *storageDataServer) Get(ctx context.Context, in *proto.StorageKeyRequest) (*proto.StorageGetResponse, error) {
	v, err := s.impl.Get(ctx, in.TenantId, in.Table, in.Key)
	return &proto.StorageGetResponse{Values: v}, err
}
func (s *storageDataServer) Delete(ctx context.Context, in *proto.StorageKeyRequest) (*proto.StorageEmpty, error) {
	err := s.impl.Delete(ctx, in.TenantId, in.Table, in.Key)
	return &proto.StorageEmpty{}, err
}
func (s *storageDataServer) List(ctx context.Context, in *proto.StorageListRequest) (*proto.StorageListResponse, error) {
	rows, err := s.impl.List(ctx, in.TenantId, in.Table, in.After, int(in.Limit))
	if err != nil {
		return nil, err
	}
	out := &proto.StorageListResponse{}
	for _, r := range rows {
		out.Records = append(out.Records, &proto.StorageRecord{Key: r.Key, Values: r.Values})
	}
	return out, nil
}

type storageClient struct{ client proto.StorageDataClient }

func (c *storageClient) Put(ctx context.Context, tenant, table, key string, values json.RawMessage) error {
	_, err := c.client.Put(ctx, &proto.StoragePutRequest{TenantId: tenant, Table: table, Key: key, Values: values})
	return err
}
func (c *storageClient) Get(ctx context.Context, tenant, table, key string) (json.RawMessage, error) {
	r, err := c.client.Get(ctx, &proto.StorageKeyRequest{TenantId: tenant, Table: table, Key: key})
	if err != nil {
		return nil, err
	}
	return r.Values, nil
}
func (c *storageClient) Delete(ctx context.Context, tenant, table, key string) error {
	_, err := c.client.Delete(ctx, &proto.StorageKeyRequest{TenantId: tenant, Table: table, Key: key})
	return err
}
func (c *storageClient) List(ctx context.Context, tenant, table, after string, limit int) ([]StorageRecord, error) {
	// Validate before narrowing to int32 on the wire.
	if limit < 0 || limit > 25 {
		return nil, status.Error(codes.InvalidArgument, "storage list limit must be 0..25")
	}
	r, err := c.client.List(ctx, &proto.StorageListRequest{TenantId: tenant, Table: table, After: after, Limit: int32(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]StorageRecord, 0, len(r.Records))
	for _, row := range r.Records {
		out = append(out, StorageRecord{Key: row.Key, Values: row.Values})
	}
	return out, nil
}
