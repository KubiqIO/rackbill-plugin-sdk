package plugin

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"rackbill.com/sdk/plugin/proto"
)

// Contract 10 (decisions 140 to 143): private networks, the provider's own
// backups, boot modes and customer ISOs. Every read reports Offered: false
// when the product does not open the feature to customers, and the host
// then shows nothing. An action on a feature not offered returns Refused.

// Private network actions (VMPrivateNetworkChange.Action).
const (
	PrivateNetworkCreate = "create" // Name and CIDR; the server joins it
	PrivateNetworkAttach = "attach"
	PrivateNetworkDetach = "detach"
	PrivateNetworkDelete = "delete" // refused while another server is on it
)

// Provider backup actions, statuses and kinds.
const (
	VMBackupCreate      = "create"
	VMBackupRestore     = "restore" // overwrites the server's disks
	VMBackupDelete      = "delete"
	VMBackupScheduleOn  = "schedule_on"
	VMBackupScheduleOff = "schedule_off"

	VMBackupPending = "pending"
	VMBackupReady   = "ready"
	VMBackupFailed  = "failed"

	VMBackupManual    = "manual"
	VMBackupScheduled = "scheduled"
)

// Boot modes and ISO actions.
const (
	VMBootDisk   = "disk"
	VMBootRescue = "rescue"
	VMBootISO    = "iso"

	VMISOAdd    = "add"    // Name and an https URL the provider fetches
	VMISODelete = "delete" // one the server's owner owns
)

// VMPrivateNetwork is one private network of the server's owner.
type VMPrivateNetwork struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	CIDR        string `json:"cidr"`
	Attached    bool   `json:"attached"`
	Address     string `json:"address,omitempty"`
	ServerCount int    `json:"server_count"`
}

// VMPrivateNetworks is the owner's networks in the server's location.
type VMPrivateNetworks struct {
	Offered   bool               `json:"offered"`
	Networks  []VMPrivateNetwork `json:"networks"`
	CanCreate bool               `json:"can_create"`
}

// VMPrivateNetworkChange is one private network action. The agent has
// checked CIDR is an RFC 1918 network on create.
type VMPrivateNetworkChange struct {
	Action    string
	NetworkID string
	Name      string
	CIDR      string
}

// VMPrivateNetworker is the optional private networking of a VM-capable
// provisioner. Implement it and set SupportsPrivateNetworks.
type VMPrivateNetworker interface {
	VMPrivateNetworks(ctx context.Context, ref VMRef) (VMPrivateNetworks, error)
	VMPrivateNetworkAction(ctx context.Context, ref VMRef, change VMPrivateNetworkChange) error
}

// VMBackup is one backup the provider keeps of the server.
type VMBackup struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	Kind        string `json:"kind"`
	Type        string `json:"type,omitempty"`
	SizeBytes   int64  `json:"size_bytes"`
	CreatedUnix int64  `json:"created_unix"`
	Restorable  bool   `json:"restorable"`
	Note        string `json:"note,omitempty"`
}

// VMBackups is the server's provider backups and its schedule.
type VMBackups struct {
	Offered         bool       `json:"offered"`
	Backups         []VMBackup `json:"backups"`
	Limit           int        `json:"limit"`
	ScheduleEnabled bool       `json:"schedule_enabled"`
	Schedule        string     `json:"schedule,omitempty"`
	CanSchedule     bool       `json:"can_schedule"`
	NextUnix        int64      `json:"next_unix,omitempty"`
}

// VMBackupper is the optional provider-side backup of a VM-capable
// provisioner. Implement it and set SupportsProviderBackups. It is not the
// repository backup capability (SupportsBackup): these backups live on the
// provider's own storage.
type VMBackupper interface {
	VMBackups(ctx context.Context, ref VMRef) (VMBackups, error)
	VMBackupAction(ctx context.Context, ref VMRef, action, backupID string) error
}

// VMISOImage is one ISO the server may boot.
type VMISOImage struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	SizeBytes int64  `json:"size_bytes"`
	Own       bool   `json:"own"`
}

// VMBootState is what the server boots from next and what it may boot.
type VMBootState struct {
	Offered   bool         `json:"offered"`
	Mode      string       `json:"mode"`
	ISOID     string       `json:"iso_id,omitempty"`
	ISOs      []VMISOImage `json:"isos"`
	CanAddISO bool         `json:"can_add_iso"`
	ISOLimit  int          `json:"iso_limit"`
}

// VMBootModer is the optional boot-mode switch of a VM-capable provisioner.
// Implement it and set SupportsBootModes. VMSetBootMode does not restart
// the server; the host does when the customer asks.
type VMBootModer interface {
	VMBootMode(ctx context.Context, ref VMRef) (VMBootState, error)
	VMSetBootMode(ctx context.Context, ref VMRef, mode, isoID string) error
}

// VMISOChange is one ISO action.
type VMISOChange struct {
	Action string
	ISOID  string
	Name   string
	URL    string
}

// VMISOManager adds and deletes the server owner's own ISOs. Implement it
// and set SupportsISOImages (with SupportsBootModes).
type VMISOManager interface {
	VMISOAction(ctx context.Context, ref VMRef, change VMISOChange) error
}

// --- plugin side ---------------------------------------------------------------

func (s *provisionerGRPCServer) GetVMPrivateNetworks(ctx context.Context, req *proto.VMRequest) (*proto.VMPrivateNetworksResponse, error) {
	m, ok := s.impl.(VMPrivateNetworker)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "plugin does not implement the private-network capability")
	}
	n, err := m.VMPrivateNetworks(ctx, vmRefFromProto(req))
	if err != nil {
		return nil, refusedToStatus(err)
	}
	out := &proto.VMPrivateNetworksResponse{Offered: n.Offered, CanCreate: n.CanCreate, Networks: make([]*proto.VMPrivateNetwork, len(n.Networks))}
	for i, x := range n.Networks {
		out.Networks[i] = &proto.VMPrivateNetwork{Id: x.ID, Name: x.Name, Cidr: x.CIDR, Attached: x.Attached, Address: x.Address, ServerCount: int32(x.ServerCount)}
	}
	return out, nil
}

func (s *provisionerGRPCServer) VMPrivateNetworkAction(ctx context.Context, req *proto.VMPrivateNetworkRequest) (*proto.VMEmpty, error) {
	m, ok := s.impl.(VMPrivateNetworker)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "plugin does not implement the private-network capability")
	}
	err := m.VMPrivateNetworkAction(ctx, vmRefFromProto(req.Vm), VMPrivateNetworkChange{Action: req.Action, NetworkID: req.NetworkId, Name: req.Name, CIDR: req.Cidr})
	if err != nil {
		return nil, refusedToStatus(err)
	}
	return &proto.VMEmpty{}, nil
}

func (s *provisionerGRPCServer) GetVMBackups(ctx context.Context, req *proto.VMRequest) (*proto.VMBackupsResponse, error) {
	m, ok := s.impl.(VMBackupper)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "plugin does not implement the provider-backup capability")
	}
	b, err := m.VMBackups(ctx, vmRefFromProto(req))
	if err != nil {
		return nil, refusedToStatus(err)
	}
	out := &proto.VMBackupsResponse{Offered: b.Offered, Limit: int32(b.Limit), ScheduleEnabled: b.ScheduleEnabled, Schedule: b.Schedule,
		CanSchedule: b.CanSchedule, NextUnix: b.NextUnix, Backups: make([]*proto.VMBackup, len(b.Backups))}
	for i, x := range b.Backups {
		out.Backups[i] = &proto.VMBackup{Id: x.ID, Status: x.Status, Kind: x.Kind, Type: x.Type, SizeBytes: x.SizeBytes,
			CreatedUnix: x.CreatedUnix, Restorable: x.Restorable, Note: x.Note}
	}
	return out, nil
}

func (s *provisionerGRPCServer) VMBackupAction(ctx context.Context, req *proto.VMBackupActionRequest) (*proto.VMEmpty, error) {
	m, ok := s.impl.(VMBackupper)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "plugin does not implement the provider-backup capability")
	}
	if err := m.VMBackupAction(ctx, vmRefFromProto(req.Vm), req.Action, req.BackupId); err != nil {
		return nil, refusedToStatus(err)
	}
	return &proto.VMEmpty{}, nil
}

func (s *provisionerGRPCServer) GetVMBootMode(ctx context.Context, req *proto.VMRequest) (*proto.VMBootModeResponse, error) {
	m, ok := s.impl.(VMBootModer)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "plugin does not implement the boot-mode capability")
	}
	b, err := m.VMBootMode(ctx, vmRefFromProto(req))
	if err != nil {
		return nil, refusedToStatus(err)
	}
	out := &proto.VMBootModeResponse{Offered: b.Offered, Mode: b.Mode, IsoId: b.ISOID, CanAddIso: b.CanAddISO,
		IsoLimit: int32(b.ISOLimit), Isos: make([]*proto.VMISOImage, len(b.ISOs))}
	for i, x := range b.ISOs {
		out.Isos[i] = &proto.VMISOImage{Id: x.ID, Name: x.Name, SizeBytes: x.SizeBytes, Own: x.Own}
	}
	return out, nil
}

func (s *provisionerGRPCServer) SetVMBootMode(ctx context.Context, req *proto.VMBootModeRequest) (*proto.VMEmpty, error) {
	m, ok := s.impl.(VMBootModer)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "plugin does not implement the boot-mode capability")
	}
	if err := m.VMSetBootMode(ctx, vmRefFromProto(req.Vm), req.Mode, req.IsoId); err != nil {
		return nil, refusedToStatus(err)
	}
	return &proto.VMEmpty{}, nil
}

func (s *provisionerGRPCServer) VMISOAction(ctx context.Context, req *proto.VMISOActionRequest) (*proto.VMEmpty, error) {
	m, ok := s.impl.(VMISOManager)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "plugin does not implement the ISO capability")
	}
	if err := m.VMISOAction(ctx, vmRefFromProto(req.Vm), VMISOChange{Action: req.Action, ISOID: req.IsoId, Name: req.Name, URL: req.Url}); err != nil {
		return nil, refusedToStatus(err)
	}
	return &proto.VMEmpty{}, nil
}

// --- host side -----------------------------------------------------------------

func (c *provisionerGRPCClient) VMPrivateNetworks(ctx context.Context, ref VMRef) (VMPrivateNetworks, error) {
	resp, err := c.client.GetVMPrivateNetworks(ctx, vmRefToProto(ref))
	if err != nil {
		return VMPrivateNetworks{}, refusedFromStatus(err)
	}
	out := VMPrivateNetworks{Offered: resp.Offered, CanCreate: resp.CanCreate, Networks: make([]VMPrivateNetwork, len(resp.Networks))}
	for i, x := range resp.Networks {
		out.Networks[i] = VMPrivateNetwork{ID: x.Id, Name: x.Name, CIDR: x.Cidr, Attached: x.Attached, Address: x.Address, ServerCount: int(x.ServerCount)}
	}
	return out, nil
}

func (c *provisionerGRPCClient) VMPrivateNetworkAction(ctx context.Context, ref VMRef, change VMPrivateNetworkChange) error {
	_, err := c.client.VMPrivateNetworkAction(ctx, &proto.VMPrivateNetworkRequest{Vm: vmRefToProto(ref), Action: change.Action,
		NetworkId: change.NetworkID, Name: change.Name, Cidr: change.CIDR})
	return refusedFromStatus(err)
}

func (c *provisionerGRPCClient) VMBackups(ctx context.Context, ref VMRef) (VMBackups, error) {
	resp, err := c.client.GetVMBackups(ctx, vmRefToProto(ref))
	if err != nil {
		return VMBackups{}, refusedFromStatus(err)
	}
	out := VMBackups{Offered: resp.Offered, Limit: int(resp.Limit), ScheduleEnabled: resp.ScheduleEnabled, Schedule: resp.Schedule,
		CanSchedule: resp.CanSchedule, NextUnix: resp.NextUnix, Backups: make([]VMBackup, len(resp.Backups))}
	for i, x := range resp.Backups {
		out.Backups[i] = VMBackup{ID: x.Id, Status: x.Status, Kind: x.Kind, Type: x.Type, SizeBytes: x.SizeBytes,
			CreatedUnix: x.CreatedUnix, Restorable: x.Restorable, Note: x.Note}
	}
	return out, nil
}

func (c *provisionerGRPCClient) VMBackupAction(ctx context.Context, ref VMRef, action, backupID string) error {
	_, err := c.client.VMBackupAction(ctx, &proto.VMBackupActionRequest{Vm: vmRefToProto(ref), Action: action, BackupId: backupID})
	return refusedFromStatus(err)
}

func (c *provisionerGRPCClient) VMBootMode(ctx context.Context, ref VMRef) (VMBootState, error) {
	resp, err := c.client.GetVMBootMode(ctx, vmRefToProto(ref))
	if err != nil {
		return VMBootState{}, refusedFromStatus(err)
	}
	out := VMBootState{Offered: resp.Offered, Mode: resp.Mode, ISOID: resp.IsoId, CanAddISO: resp.CanAddIso,
		ISOLimit: int(resp.IsoLimit), ISOs: make([]VMISOImage, len(resp.Isos))}
	for i, x := range resp.Isos {
		out.ISOs[i] = VMISOImage{ID: x.Id, Name: x.Name, SizeBytes: x.SizeBytes, Own: x.Own}
	}
	return out, nil
}

func (c *provisionerGRPCClient) VMSetBootMode(ctx context.Context, ref VMRef, mode, isoID string) error {
	_, err := c.client.SetVMBootMode(ctx, &proto.VMBootModeRequest{Vm: vmRefToProto(ref), Mode: mode, IsoId: isoID})
	return refusedFromStatus(err)
}

func (c *provisionerGRPCClient) VMISOAction(ctx context.Context, ref VMRef, change VMISOChange) error {
	_, err := c.client.VMISOAction(ctx, &proto.VMISOActionRequest{Vm: vmRefToProto(ref), Action: change.Action,
		IsoId: change.ISOID, Name: change.Name, Url: change.URL})
	return refusedFromStatus(err)
}
