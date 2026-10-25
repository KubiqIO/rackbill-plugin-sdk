package plugin

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"kubiq.io/rackbill/sdk/plugin/proto"
)

// The game server capability (§7.15): the end-user surface of a game panel
// — console, files, backups, databases, schedules, network, sub-users,
// startup, settings, activity — reproduced in the portal. Power and state
// ride the VM capability, so SupportsGameServer implies SupportsVM. One
// reader and one action call per sub-resource; declared refusals travel as
// RefusedError.

// Game server sub-resources a GameServer may list in Features. The portal
// renders a tab for each one present.
const (
	GameFeatureConsole   = "console"
	GameFeatureCommand   = "command"
	GameFeatureFiles     = "files"
	GameFeatureBackups   = "backups"
	GameFeatureDatabases = "databases"
	GameFeatureSchedules = "schedules"
	GameFeatureNetwork   = "network"
	GameFeatureUsers     = "users"
	GameFeatureStartup   = "startup"
	GameFeatureSettings  = "settings"
	GameFeatureActivity  = "activity"
)

// Console protocols a GameConsole may name. The agent bridges the ones it
// can speak and refuses the rest.
const (
	// GameConsoleWings is the Pterodactyl / Pelican daemon dialect: JSON
	// {"event","args"} frames — auth, console output, stats, status, set
	// state, send command.
	GameConsoleWings = "wings"
)

// Game file actions.
const (
	GameFileRename     = "rename"
	GameFileCopy       = "copy"
	GameFileDelete     = "delete"
	GameFileMkdir      = "mkdir"
	GameFileCompress   = "compress"
	GameFileDecompress = "decompress"
	GameFileChmod      = "chmod"
	GameFilePull       = "pull"
	GameFileDownload   = "download"
	GameFileUpload     = "upload"
)

// Game backup actions.
const (
	GameBackupCreate   = "create"
	GameBackupDelete   = "delete"
	GameBackupLock     = "lock"
	GameBackupUnlock   = "unlock"
	GameBackupRestore  = "restore"
	GameBackupDownload = "download"
)

// Game database actions.
const (
	GameDatabaseCreate = "create"
	GameDatabaseRotate = "rotate"
	GameDatabaseDelete = "delete"
)

// Game schedule / task / user actions.
const (
	GameActionSave    = "save"
	GameActionDelete  = "delete"
	GameActionExecute = "execute"
)

// Game allocation actions.
const (
	GameAllocationAdd     = "add"
	GameAllocationNotes   = "notes"
	GameAllocationPrimary = "primary"
	GameAllocationDelete  = "delete"
)

// Game settings actions.
const (
	GameSettingsRename      = "rename"
	GameSettingsReinstall   = "reinstall"
	GameSettingsDockerImage = "docker_image"
)

// RefusedError is a declared failure on a game-server call: the remote
// panel understood the request and said no, for a reason the user should
// read verbatim ("The server has reached its backup limit."). Transport
// problems — unreachable panel, bad credentials, a 500 — stay plain errors
// and surface as "could not reach the panel". Over gRPC a refusal travels as
// FailedPrecondition and comes back as a RefusedError on the host side.
type RefusedError struct {
	Reason string
}

func (e *RefusedError) Error() string { return e.Reason }

// Refused builds a RefusedError.
func Refused(format string, args ...any) error {
	return &RefusedError{Reason: fmt.Sprintf(format, args...)}
}

// IsRefused reports whether err is (or wraps) a declared refusal and, if so,
// the reason to show the user.
func IsRefused(err error) (string, bool) {
	var re *RefusedError
	if errors.As(err, &re) {
		return re.Reason, true
	}
	return "", false
}

// refusedToStatus maps a refusal onto the wire; every other error passes
// through unchanged.
func refusedToStatus(err error) error {
	if reason, ok := IsRefused(err); ok {
		return status.Error(codes.FailedPrecondition, reason)
	}
	return err
}

// refusedFromStatus is the inverse: a FailedPrecondition from the plugin is
// the panel's declared refusal.
func refusedFromStatus(err error) error {
	if err == nil {
		return nil
	}
	if st, ok := status.FromError(err); ok && st.Code() == codes.FailedPrecondition {
		return &RefusedError{Reason: st.Message()}
	}
	return err
}

// GameRef identifies a provisioned game server on game-capability calls;
// the same shape as VMRef.
type GameRef struct {
	TenantID    string
	ServiceID   string
	ExternalRef string
	Config      map[string]string
}

// GameLimits is the server's resource envelope as the panel enforces it.
type GameLimits struct {
	MemoryBytes int64  `json:"memory_bytes"` // 0 = unlimited
	SwapBytes   int64  `json:"swap_bytes"`   // -1 = unlimited, 0 = none
	DiskBytes   int64  `json:"disk_bytes"`   // 0 = unlimited
	CPUPct      int64  `json:"cpu_pct"`      // 100 = one core; 0 = unlimited
	IOWeight    int64  `json:"io_weight"`
	Threads     string `json:"threads,omitempty"`
	OOMKiller   bool   `json:"oom_killer"`
}

type GameFeatureLimits struct {
	Databases   int `json:"databases"`
	Backups     int `json:"backups"`
	Allocations int `json:"allocations"`
}

// GameServer is the server as the panel describes it — the landing call of
// the dashboard. Features lists the sub-resources this particular server
// offers (a zero backup limit drops "backups"); the portal renders tabs for
// those and nothing else.
type GameServer struct {
	Name          string            `json:"name"`
	Description   string            `json:"description"`
	Status        string            `json:"status"` // "", installing, install_failed, suspended, restoring_backup, transferring
	Address       string            `json:"address"`
	SFTPHost      string            `json:"sftp_host"`
	SFTPPort      int               `json:"sftp_port"`
	SFTPUsername  string            `json:"sftp_username"`
	Limits        GameLimits        `json:"limits"`
	FeatureLimits GameFeatureLimits `json:"feature_limits"`
	Features      []string          `json:"features"`
	DockerImage   string            `json:"docker_image"`
	Invocation    string            `json:"invocation"`
	PanelURL      string            `json:"panel_url"`
	Details       map[string]string `json:"details"`
}

// GameConsole is what the agent needs to bridge a browser to the daemon's
// console websocket. The token never reaches the browser: the agent
// authenticates upstream and re-mints through GameConsole when the daemon
// announces expiry.
type GameConsole struct {
	Protocol           string
	WebsocketURL       string
	Headers            map[string]string
	Token              string
	ExpiresUnix        int64
	CAPEM              string
	InsecureSkipVerify bool
}

type GameFile struct {
	Name         string `json:"name"`
	Size         int64  `json:"size"`
	IsFile       bool   `json:"is_file"`
	IsSymlink    bool   `json:"is_symlink"`
	Mode         string `json:"mode"`
	ModeBits     string `json:"mode_bits"`
	MimeType     string `json:"mime_type"`
	ModifiedUnix int64  `json:"modified_unix"`
	CreatedUnix  int64  `json:"created_unix"`
}

type GameRename struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// GameFileAction is one file operation; which fields matter depends on
// Action (see the proto comment): rename uses Root+Renames, copy Files[0],
// delete Root+Files, mkdir Root+Name, compress Root+Files, decompress
// Root+Files[0], chmod Root+Files[0]+Mode, pull Root+URL+Name, download
// Files[0], upload Root.
type GameFileAction struct {
	Action  string
	Root    string
	Files   []string
	Renames []GameRename
	Name    string
	Mode    string
	URL     string
}

type GameFileActionResult struct {
	File *GameFile `json:"file,omitempty"` // compress: the archive
	URL  string    `json:"url,omitempty"`  // download / upload: signed daemon URL
}

type GameBackup struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	IgnoredFiles  []string `json:"ignored_files"`
	Bytes         int64    `json:"bytes"`
	Checksum      string   `json:"checksum"`
	Successful    bool     `json:"successful"`
	Locked        bool     `json:"locked"`
	Completed     bool     `json:"completed"`
	CreatedUnix   int64    `json:"created_unix"`
	CompletedUnix int64    `json:"completed_unix"`
}

type GameBackupList struct {
	Backups []GameBackup `json:"backups"`
	Limit   int          `json:"limit"`
}

type GameBackupAction struct {
	Action   string
	ID       string
	Name     string
	Ignored  string
	Locked   bool
	Truncate bool
}

type GameBackupActionResult struct {
	Backup *GameBackup `json:"backup,omitempty"`
	URL    string      `json:"url,omitempty"`
}

type GameDatabase struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Username        string `json:"username"`
	Password        string `json:"password"`
	Host            string `json:"host"`
	Port            int    `json:"port"`
	ConnectionsFrom string `json:"connections_from"`
	MaxConnections  int    `json:"max_connections"`
}

type GameDatabaseList struct {
	Databases []GameDatabase `json:"databases"`
	Limit     int            `json:"limit"`
}

type GameDatabaseAction struct {
	Action string
	ID     string
	Name   string
	Remote string
}

type GameCron struct {
	Minute     string `json:"minute"`
	Hour       string `json:"hour"`
	DayOfMonth string `json:"day_of_month"`
	Month      string `json:"month"`
	DayOfWeek  string `json:"day_of_week"`
}

type GameTask struct {
	ID                string `json:"id"`
	Sequence          int    `json:"sequence"`
	Action            string `json:"action"` // command, power, backup
	Payload           string `json:"payload"`
	TimeOffset        int    `json:"time_offset"`
	ContinueOnFailure bool   `json:"continue_on_failure"`
	Queued            bool   `json:"queued"`
}

type GameSchedule struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	Cron           GameCron   `json:"cron"`
	Active         bool       `json:"active"`
	Processing     bool       `json:"processing"`
	OnlyWhenOnline bool       `json:"only_when_online"`
	LastRunUnix    int64      `json:"last_run_unix"`
	NextRunUnix    int64      `json:"next_run_unix"`
	CreatedUnix    int64      `json:"created_unix"`
	Tasks          []GameTask `json:"tasks"`
}

type GameScheduleAction struct {
	Action         string
	ID             string
	Name           string
	Cron           GameCron
	Active         bool
	OnlyWhenOnline bool
}

type GameTaskAction struct {
	Action            string
	ScheduleID        string
	ID                string
	TaskAction        string
	Payload           string
	TimeOffset        int
	ContinueOnFailure bool
}

type GameAllocation struct {
	ID      string `json:"id"`
	IP      string `json:"ip"`
	Alias   string `json:"alias"`
	Port    int    `json:"port"`
	Notes   string `json:"notes"`
	Primary bool   `json:"primary"`
}

type GameAllocationList struct {
	Allocations []GameAllocation `json:"allocations"`
	Limit       int              `json:"limit"`
}

type GameAllocationAction struct {
	Action string
	ID     string
	Notes  string
}

type GameUser struct {
	ID          string   `json:"id"`
	Username    string   `json:"username"`
	Email       string   `json:"email"`
	Image       string   `json:"image"`
	TwoFactor   bool     `json:"two_factor"`
	Permissions []string `json:"permissions"`
	CreatedUnix int64    `json:"created_unix"`
}

type GamePermission struct {
	Key         string `json:"key"`
	Description string `json:"description"`
}

type GamePermissionGroup struct {
	Key         string           `json:"key"`
	Description string           `json:"description"`
	Permissions []GamePermission `json:"permissions"`
}

type GameUserList struct {
	Users   []GameUser            `json:"users"`
	Catalog []GamePermissionGroup `json:"catalog"`
}

type GameUserAction struct {
	Action      string
	ID          string
	Email       string
	Permissions []string
}

type GameVariable struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	EnvVariable  string `json:"env_variable"`
	DefaultValue string `json:"default_value"`
	Value        string `json:"value"`
	Editable     bool   `json:"editable"`
	Rules        string `json:"rules"`
}

type GameStartup struct {
	Command      string            `json:"command"`
	RawCommand   string            `json:"raw_command"`
	Variables    []GameVariable    `json:"variables"`
	DockerImage  string            `json:"docker_image"`
	DockerImages map[string]string `json:"docker_images"`
}

type GameSettingsAction struct {
	Action      string
	Name        string
	Description string
	DockerImage string
}

type GameActivity struct {
	ID            string            `json:"id"`
	Event         string            `json:"event"`
	Description   string            `json:"description"`
	Actor         string            `json:"actor"`
	IP            string            `json:"ip"`
	API           bool              `json:"api"`
	Properties    map[string]string `json:"properties"`
	TimestampUnix int64             `json:"timestamp_unix"`
}

type GameActivityPage struct {
	Entries    []GameActivity `json:"entries"`
	Page       int            `json:"page"`
	TotalPages int            `json:"total_pages"`
}

// GameServerManager is the optional game-server capability of a provisioner.
// Implement it alongside Provisioner (and VMManager — power and state ride
// there) and set SupportsGameServer in Describe; the host never calls these
// without the flag. Every call is interactive: a person is looking at the
// dashboard, so budgets are seconds, and a panel's refusal should come back
// as Refused(...) with the panel's own words.
type GameServerManager interface {
	GameServer(ctx context.Context, ref GameRef) (GameServer, error)
	GameConsole(ctx context.Context, ref GameRef) (GameConsole, error)
	GameCommand(ctx context.Context, ref GameRef, command string) error
	GameFiles(ctx context.Context, ref GameRef, directory string) ([]GameFile, error)
	GameFileRead(ctx context.Context, ref GameRef, path string) ([]byte, error)
	GameFileWrite(ctx context.Context, ref GameRef, path string, content []byte) error
	GameFileAction(ctx context.Context, ref GameRef, in GameFileAction) (GameFileActionResult, error)
	GameBackups(ctx context.Context, ref GameRef) (GameBackupList, error)
	GameBackupAction(ctx context.Context, ref GameRef, in GameBackupAction) (GameBackupActionResult, error)
	GameDatabases(ctx context.Context, ref GameRef) (GameDatabaseList, error)
	GameDatabaseAction(ctx context.Context, ref GameRef, in GameDatabaseAction) (*GameDatabase, error)
	GameSchedules(ctx context.Context, ref GameRef) ([]GameSchedule, error)
	GameScheduleAction(ctx context.Context, ref GameRef, in GameScheduleAction) (*GameSchedule, error)
	GameTaskAction(ctx context.Context, ref GameRef, in GameTaskAction) (*GameTask, error)
	GameAllocations(ctx context.Context, ref GameRef) (GameAllocationList, error)
	GameAllocationAction(ctx context.Context, ref GameRef, in GameAllocationAction) (*GameAllocation, error)
	GameUsers(ctx context.Context, ref GameRef) (GameUserList, error)
	GameUserAction(ctx context.Context, ref GameRef, in GameUserAction) (*GameUser, error)
	GameStartup(ctx context.Context, ref GameRef) (GameStartup, error)
	GameVariableSet(ctx context.Context, ref GameRef, key, value string) (GameVariable, error)
	GameSettings(ctx context.Context, ref GameRef, in GameSettingsAction) error
	GameActivity(ctx context.Context, ref GameRef, page int) (GameActivityPage, error)
}

// --- wire conversion ---

func gameRefFromProto(r *proto.GameRef) GameRef {
	if r == nil {
		return GameRef{}
	}
	return GameRef{TenantID: r.TenantId, ServiceID: r.ServiceId, ExternalRef: r.ExternalRef, Config: r.Config}
}

func gameRefToProto(r GameRef) *proto.GameRef {
	return &proto.GameRef{TenantId: r.TenantID, ServiceId: r.ServiceID, ExternalRef: r.ExternalRef, Config: r.Config}
}

func gameFileToProto(f GameFile) *proto.GameFile {
	return &proto.GameFile{
		Name: f.Name, Size: f.Size, IsFile: f.IsFile, IsSymlink: f.IsSymlink,
		Mode: f.Mode, ModeBits: f.ModeBits, MimeType: f.MimeType,
		ModifiedUnix: f.ModifiedUnix, CreatedUnix: f.CreatedUnix,
	}
}

func gameFileFromProto(f *proto.GameFile) GameFile {
	if f == nil {
		return GameFile{}
	}
	return GameFile{
		Name: f.Name, Size: f.Size, IsFile: f.IsFile, IsSymlink: f.IsSymlink,
		Mode: f.Mode, ModeBits: f.ModeBits, MimeType: f.MimeType,
		ModifiedUnix: f.ModifiedUnix, CreatedUnix: f.CreatedUnix,
	}
}

func gameBackupToProto(b GameBackup) *proto.GameBackup {
	return &proto.GameBackup{
		Id: b.ID, Name: b.Name, IgnoredFiles: b.IgnoredFiles, Bytes: b.Bytes, Checksum: b.Checksum,
		Successful: b.Successful, Locked: b.Locked, Completed: b.Completed,
		CreatedUnix: b.CreatedUnix, CompletedUnix: b.CompletedUnix,
	}
}

func gameBackupFromProto(b *proto.GameBackup) GameBackup {
	if b == nil {
		return GameBackup{}
	}
	return GameBackup{
		ID: b.Id, Name: b.Name, IgnoredFiles: b.IgnoredFiles, Bytes: b.Bytes, Checksum: b.Checksum,
		Successful: b.Successful, Locked: b.Locked, Completed: b.Completed,
		CreatedUnix: b.CreatedUnix, CompletedUnix: b.CompletedUnix,
	}
}

func gameDatabaseToProto(d GameDatabase) *proto.GameDatabase {
	return &proto.GameDatabase{
		Id: d.ID, Name: d.Name, Username: d.Username, Password: d.Password, Host: d.Host,
		Port: int32(d.Port), ConnectionsFrom: d.ConnectionsFrom, MaxConnections: int32(d.MaxConnections),
	}
}

func gameDatabaseFromProto(d *proto.GameDatabase) GameDatabase {
	if d == nil {
		return GameDatabase{}
	}
	return GameDatabase{
		ID: d.Id, Name: d.Name, Username: d.Username, Password: d.Password, Host: d.Host,
		Port: int(d.Port), ConnectionsFrom: d.ConnectionsFrom, MaxConnections: int(d.MaxConnections),
	}
}

func gameCronToProto(c GameCron) *proto.GameCron {
	return &proto.GameCron{Minute: c.Minute, Hour: c.Hour, DayOfMonth: c.DayOfMonth, Month: c.Month, DayOfWeek: c.DayOfWeek}
}

func gameCronFromProto(c *proto.GameCron) GameCron {
	if c == nil {
		return GameCron{}
	}
	return GameCron{Minute: c.Minute, Hour: c.Hour, DayOfMonth: c.DayOfMonth, Month: c.Month, DayOfWeek: c.DayOfWeek}
}

func gameTaskToProto(t GameTask) *proto.GameTask {
	return &proto.GameTask{
		Id: t.ID, Sequence: int32(t.Sequence), Action: t.Action, Payload: t.Payload,
		TimeOffset: int32(t.TimeOffset), ContinueOnFailure: t.ContinueOnFailure, Queued: t.Queued,
	}
}

func gameTaskFromProto(t *proto.GameTask) GameTask {
	if t == nil {
		return GameTask{}
	}
	return GameTask{
		ID: t.Id, Sequence: int(t.Sequence), Action: t.Action, Payload: t.Payload,
		TimeOffset: int(t.TimeOffset), ContinueOnFailure: t.ContinueOnFailure, Queued: t.Queued,
	}
}

func gameScheduleToProto(s GameSchedule) *proto.GameSchedule {
	tasks := make([]*proto.GameTask, len(s.Tasks))
	for i, t := range s.Tasks {
		tasks[i] = gameTaskToProto(t)
	}
	return &proto.GameSchedule{
		Id: s.ID, Name: s.Name, Cron: gameCronToProto(s.Cron), Active: s.Active, Processing: s.Processing,
		OnlyWhenOnline: s.OnlyWhenOnline, LastRunUnix: s.LastRunUnix, NextRunUnix: s.NextRunUnix,
		CreatedUnix: s.CreatedUnix, Tasks: tasks,
	}
}

func gameScheduleFromProto(s *proto.GameSchedule) GameSchedule {
	if s == nil {
		return GameSchedule{}
	}
	tasks := make([]GameTask, len(s.Tasks))
	for i, t := range s.Tasks {
		tasks[i] = gameTaskFromProto(t)
	}
	return GameSchedule{
		ID: s.Id, Name: s.Name, Cron: gameCronFromProto(s.Cron), Active: s.Active, Processing: s.Processing,
		OnlyWhenOnline: s.OnlyWhenOnline, LastRunUnix: s.LastRunUnix, NextRunUnix: s.NextRunUnix,
		CreatedUnix: s.CreatedUnix, Tasks: tasks,
	}
}

func gameAllocationToProto(a GameAllocation) *proto.GameAllocation {
	return &proto.GameAllocation{Id: a.ID, Ip: a.IP, Alias: a.Alias, Port: int32(a.Port), Notes: a.Notes, Primary: a.Primary}
}

func gameAllocationFromProto(a *proto.GameAllocation) GameAllocation {
	if a == nil {
		return GameAllocation{}
	}
	return GameAllocation{ID: a.Id, IP: a.Ip, Alias: a.Alias, Port: int(a.Port), Notes: a.Notes, Primary: a.Primary}
}

func gameUserToProto(u GameUser) *proto.GameUser {
	return &proto.GameUser{
		Id: u.ID, Username: u.Username, Email: u.Email, Image: u.Image, TwoFactor: u.TwoFactor,
		Permissions: u.Permissions, CreatedUnix: u.CreatedUnix,
	}
}

func gameUserFromProto(u *proto.GameUser) GameUser {
	if u == nil {
		return GameUser{}
	}
	return GameUser{
		ID: u.Id, Username: u.Username, Email: u.Email, Image: u.Image, TwoFactor: u.TwoFactor,
		Permissions: u.Permissions, CreatedUnix: u.CreatedUnix,
	}
}

func gameVariableToProto(v GameVariable) *proto.GameVariable {
	return &proto.GameVariable{
		Name: v.Name, Description: v.Description, EnvVariable: v.EnvVariable, DefaultValue: v.DefaultValue,
		Value: v.Value, Editable: v.Editable, Rules: v.Rules,
	}
}

func gameVariableFromProto(v *proto.GameVariable) GameVariable {
	if v == nil {
		return GameVariable{}
	}
	return GameVariable{
		Name: v.Name, Description: v.Description, EnvVariable: v.EnvVariable, DefaultValue: v.DefaultValue,
		Value: v.Value, Editable: v.Editable, Rules: v.Rules,
	}
}

// --- gRPC server adapter (plugin side) ---

// game returns the implementation's optional game-server capability, or an
// UNIMPLEMENTED error for plugins that never declared it.
func (s *provisionerGRPCServer) game() (GameServerManager, error) {
	if m, ok := s.impl.(GameServerManager); ok {
		return m, nil
	}
	return nil, status.Error(codes.Unimplemented, "plugin does not implement the game server capability")
}

func (s *provisionerGRPCServer) GetGameServer(ctx context.Context, req *proto.GameServerRequest) (*proto.GameServerResponse, error) {
	m, err := s.game()
	if err != nil {
		return nil, err
	}
	g, err := m.GameServer(ctx, gameRefFromProto(req.Ref))
	if err != nil {
		return nil, refusedToStatus(err)
	}
	return &proto.GameServerResponse{
		Name: g.Name, Description: g.Description, Status: g.Status, Address: g.Address,
		SftpHost: g.SFTPHost, SftpPort: int32(g.SFTPPort), SftpUsername: g.SFTPUsername,
		Limits: &proto.GameLimits{
			MemoryBytes: g.Limits.MemoryBytes, SwapBytes: g.Limits.SwapBytes, DiskBytes: g.Limits.DiskBytes,
			CpuPct: g.Limits.CPUPct, IoWeight: g.Limits.IOWeight, Threads: g.Limits.Threads, OomKiller: g.Limits.OOMKiller,
		},
		FeatureLimits: &proto.GameFeatureLimits{
			Databases: int32(g.FeatureLimits.Databases), Backups: int32(g.FeatureLimits.Backups),
			Allocations: int32(g.FeatureLimits.Allocations),
		},
		Features: g.Features, DockerImage: g.DockerImage, Invocation: g.Invocation,
		PanelUrl: g.PanelURL, Details: g.Details,
	}, nil
}

func (s *provisionerGRPCServer) GetGameConsole(ctx context.Context, req *proto.GameServerRequest) (*proto.GameConsoleResponse, error) {
	m, err := s.game()
	if err != nil {
		return nil, err
	}
	c, err := m.GameConsole(ctx, gameRefFromProto(req.Ref))
	if err != nil {
		return nil, refusedToStatus(err)
	}
	return &proto.GameConsoleResponse{
		Protocol: c.Protocol, WebsocketUrl: c.WebsocketURL, Headers: c.Headers, Token: c.Token,
		ExpiresUnix: c.ExpiresUnix, CaPem: c.CAPEM, InsecureSkipVerify: c.InsecureSkipVerify,
	}, nil
}

func (s *provisionerGRPCServer) SendGameCommand(ctx context.Context, req *proto.GameCommandRequest) (*proto.GameAck, error) {
	m, err := s.game()
	if err != nil {
		return nil, err
	}
	if err := m.GameCommand(ctx, gameRefFromProto(req.Ref), req.Command); err != nil {
		return nil, refusedToStatus(err)
	}
	return &proto.GameAck{}, nil
}

func (s *provisionerGRPCServer) ListGameFiles(ctx context.Context, req *proto.GameFilesRequest) (*proto.GameFilesResponse, error) {
	m, err := s.game()
	if err != nil {
		return nil, err
	}
	files, err := m.GameFiles(ctx, gameRefFromProto(req.Ref), req.Directory)
	if err != nil {
		return nil, refusedToStatus(err)
	}
	out := make([]*proto.GameFile, len(files))
	for i, f := range files {
		out[i] = gameFileToProto(f)
	}
	return &proto.GameFilesResponse{Files: out}, nil
}

func (s *provisionerGRPCServer) ReadGameFile(ctx context.Context, req *proto.GameFileReadRequest) (*proto.GameFileReadResponse, error) {
	m, err := s.game()
	if err != nil {
		return nil, err
	}
	content, err := m.GameFileRead(ctx, gameRefFromProto(req.Ref), req.Path)
	if err != nil {
		return nil, refusedToStatus(err)
	}
	return &proto.GameFileReadResponse{Content: content}, nil
}

func (s *provisionerGRPCServer) WriteGameFile(ctx context.Context, req *proto.GameFileWriteRequest) (*proto.GameAck, error) {
	m, err := s.game()
	if err != nil {
		return nil, err
	}
	if err := m.GameFileWrite(ctx, gameRefFromProto(req.Ref), req.Path, req.Content); err != nil {
		return nil, refusedToStatus(err)
	}
	return &proto.GameAck{}, nil
}

func (s *provisionerGRPCServer) GameFileAction(ctx context.Context, req *proto.GameFileActionRequest) (*proto.GameFileActionResponse, error) {
	m, err := s.game()
	if err != nil {
		return nil, err
	}
	renames := make([]GameRename, len(req.Renames))
	for i, r := range req.Renames {
		renames[i] = GameRename{From: r.From, To: r.To}
	}
	res, err := m.GameFileAction(ctx, gameRefFromProto(req.Ref), GameFileAction{
		Action: req.Action, Root: req.Root, Files: req.Files, Renames: renames,
		Name: req.Name, Mode: req.Mode, URL: req.Url,
	})
	if err != nil {
		return nil, refusedToStatus(err)
	}
	resp := &proto.GameFileActionResponse{Url: res.URL}
	if res.File != nil {
		resp.File = gameFileToProto(*res.File)
	}
	return resp, nil
}

func (s *provisionerGRPCServer) ListGameBackups(ctx context.Context, req *proto.GameServerRequest) (*proto.GameBackupsResponse, error) {
	m, err := s.game()
	if err != nil {
		return nil, err
	}
	list, err := m.GameBackups(ctx, gameRefFromProto(req.Ref))
	if err != nil {
		return nil, refusedToStatus(err)
	}
	out := make([]*proto.GameBackup, len(list.Backups))
	for i, b := range list.Backups {
		out[i] = gameBackupToProto(b)
	}
	return &proto.GameBackupsResponse{Backups: out, Limit: int32(list.Limit)}, nil
}

func (s *provisionerGRPCServer) GameBackupAction(ctx context.Context, req *proto.GameBackupActionRequest) (*proto.GameBackupActionResponse, error) {
	m, err := s.game()
	if err != nil {
		return nil, err
	}
	res, err := m.GameBackupAction(ctx, gameRefFromProto(req.Ref), GameBackupAction{
		Action: req.Action, ID: req.Id, Name: req.Name, Ignored: req.Ignored, Locked: req.Locked, Truncate: req.Truncate,
	})
	if err != nil {
		return nil, refusedToStatus(err)
	}
	resp := &proto.GameBackupActionResponse{Url: res.URL}
	if res.Backup != nil {
		resp.Backup = gameBackupToProto(*res.Backup)
	}
	return resp, nil
}

func (s *provisionerGRPCServer) ListGameDatabases(ctx context.Context, req *proto.GameServerRequest) (*proto.GameDatabasesResponse, error) {
	m, err := s.game()
	if err != nil {
		return nil, err
	}
	list, err := m.GameDatabases(ctx, gameRefFromProto(req.Ref))
	if err != nil {
		return nil, refusedToStatus(err)
	}
	out := make([]*proto.GameDatabase, len(list.Databases))
	for i, d := range list.Databases {
		out[i] = gameDatabaseToProto(d)
	}
	return &proto.GameDatabasesResponse{Databases: out, Limit: int32(list.Limit)}, nil
}

func (s *provisionerGRPCServer) GameDatabaseAction(ctx context.Context, req *proto.GameDatabaseActionRequest) (*proto.GameDatabaseActionResponse, error) {
	m, err := s.game()
	if err != nil {
		return nil, err
	}
	db, err := m.GameDatabaseAction(ctx, gameRefFromProto(req.Ref), GameDatabaseAction{
		Action: req.Action, ID: req.Id, Name: req.Name, Remote: req.Remote,
	})
	if err != nil {
		return nil, refusedToStatus(err)
	}
	resp := &proto.GameDatabaseActionResponse{}
	if db != nil {
		resp.Database = gameDatabaseToProto(*db)
	}
	return resp, nil
}

func (s *provisionerGRPCServer) ListGameSchedules(ctx context.Context, req *proto.GameServerRequest) (*proto.GameSchedulesResponse, error) {
	m, err := s.game()
	if err != nil {
		return nil, err
	}
	schedules, err := m.GameSchedules(ctx, gameRefFromProto(req.Ref))
	if err != nil {
		return nil, refusedToStatus(err)
	}
	out := make([]*proto.GameSchedule, len(schedules))
	for i, sc := range schedules {
		out[i] = gameScheduleToProto(sc)
	}
	return &proto.GameSchedulesResponse{Schedules: out}, nil
}

func (s *provisionerGRPCServer) GameScheduleAction(ctx context.Context, req *proto.GameScheduleActionRequest) (*proto.GameScheduleActionResponse, error) {
	m, err := s.game()
	if err != nil {
		return nil, err
	}
	sc, err := m.GameScheduleAction(ctx, gameRefFromProto(req.Ref), GameScheduleAction{
		Action: req.Action, ID: req.Id, Name: req.Name, Cron: gameCronFromProto(req.Cron),
		Active: req.Active, OnlyWhenOnline: req.OnlyWhenOnline,
	})
	if err != nil {
		return nil, refusedToStatus(err)
	}
	resp := &proto.GameScheduleActionResponse{}
	if sc != nil {
		resp.Schedule = gameScheduleToProto(*sc)
	}
	return resp, nil
}

func (s *provisionerGRPCServer) GameTaskAction(ctx context.Context, req *proto.GameTaskActionRequest) (*proto.GameTaskActionResponse, error) {
	m, err := s.game()
	if err != nil {
		return nil, err
	}
	t, err := m.GameTaskAction(ctx, gameRefFromProto(req.Ref), GameTaskAction{
		Action: req.Action, ScheduleID: req.ScheduleId, ID: req.Id, TaskAction: req.TaskAction,
		Payload: req.Payload, TimeOffset: int(req.TimeOffset), ContinueOnFailure: req.ContinueOnFailure,
	})
	if err != nil {
		return nil, refusedToStatus(err)
	}
	resp := &proto.GameTaskActionResponse{}
	if t != nil {
		resp.Task = gameTaskToProto(*t)
	}
	return resp, nil
}

func (s *provisionerGRPCServer) ListGameAllocations(ctx context.Context, req *proto.GameServerRequest) (*proto.GameAllocationsResponse, error) {
	m, err := s.game()
	if err != nil {
		return nil, err
	}
	list, err := m.GameAllocations(ctx, gameRefFromProto(req.Ref))
	if err != nil {
		return nil, refusedToStatus(err)
	}
	out := make([]*proto.GameAllocation, len(list.Allocations))
	for i, a := range list.Allocations {
		out[i] = gameAllocationToProto(a)
	}
	return &proto.GameAllocationsResponse{Allocations: out, Limit: int32(list.Limit)}, nil
}

func (s *provisionerGRPCServer) GameAllocationAction(ctx context.Context, req *proto.GameAllocationActionRequest) (*proto.GameAllocationActionResponse, error) {
	m, err := s.game()
	if err != nil {
		return nil, err
	}
	a, err := m.GameAllocationAction(ctx, gameRefFromProto(req.Ref), GameAllocationAction{
		Action: req.Action, ID: req.Id, Notes: req.Notes,
	})
	if err != nil {
		return nil, refusedToStatus(err)
	}
	resp := &proto.GameAllocationActionResponse{}
	if a != nil {
		resp.Allocation = gameAllocationToProto(*a)
	}
	return resp, nil
}

func (s *provisionerGRPCServer) ListGameUsers(ctx context.Context, req *proto.GameServerRequest) (*proto.GameUsersResponse, error) {
	m, err := s.game()
	if err != nil {
		return nil, err
	}
	list, err := m.GameUsers(ctx, gameRefFromProto(req.Ref))
	if err != nil {
		return nil, refusedToStatus(err)
	}
	users := make([]*proto.GameUser, len(list.Users))
	for i, u := range list.Users {
		users[i] = gameUserToProto(u)
	}
	catalog := make([]*proto.GamePermissionGroup, len(list.Catalog))
	for i, g := range list.Catalog {
		perms := make([]*proto.GamePermission, len(g.Permissions))
		for j, p := range g.Permissions {
			perms[j] = &proto.GamePermission{Key: p.Key, Description: p.Description}
		}
		catalog[i] = &proto.GamePermissionGroup{Key: g.Key, Description: g.Description, Permissions: perms}
	}
	return &proto.GameUsersResponse{Users: users, Catalog: catalog}, nil
}

func (s *provisionerGRPCServer) GameUserAction(ctx context.Context, req *proto.GameUserActionRequest) (*proto.GameUserActionResponse, error) {
	m, err := s.game()
	if err != nil {
		return nil, err
	}
	u, err := m.GameUserAction(ctx, gameRefFromProto(req.Ref), GameUserAction{
		Action: req.Action, ID: req.Id, Email: req.Email, Permissions: req.Permissions,
	})
	if err != nil {
		return nil, refusedToStatus(err)
	}
	resp := &proto.GameUserActionResponse{}
	if u != nil {
		resp.User = gameUserToProto(*u)
	}
	return resp, nil
}

func (s *provisionerGRPCServer) GetGameStartup(ctx context.Context, req *proto.GameServerRequest) (*proto.GameStartupResponse, error) {
	m, err := s.game()
	if err != nil {
		return nil, err
	}
	st, err := m.GameStartup(ctx, gameRefFromProto(req.Ref))
	if err != nil {
		return nil, refusedToStatus(err)
	}
	vars := make([]*proto.GameVariable, len(st.Variables))
	for i, v := range st.Variables {
		vars[i] = gameVariableToProto(v)
	}
	return &proto.GameStartupResponse{
		Command: st.Command, RawCommand: st.RawCommand, Variables: vars,
		DockerImage: st.DockerImage, DockerImages: st.DockerImages,
	}, nil
}

func (s *provisionerGRPCServer) SetGameVariable(ctx context.Context, req *proto.GameVariableSetRequest) (*proto.GameVariableSetResponse, error) {
	m, err := s.game()
	if err != nil {
		return nil, err
	}
	v, err := m.GameVariableSet(ctx, gameRefFromProto(req.Ref), req.Key, req.Value)
	if err != nil {
		return nil, refusedToStatus(err)
	}
	return &proto.GameVariableSetResponse{Variable: gameVariableToProto(v)}, nil
}

func (s *provisionerGRPCServer) UpdateGameSettings(ctx context.Context, req *proto.GameSettingsRequest) (*proto.GameAck, error) {
	m, err := s.game()
	if err != nil {
		return nil, err
	}
	if err := m.GameSettings(ctx, gameRefFromProto(req.Ref), GameSettingsAction{
		Action: req.Action, Name: req.Name, Description: req.Description, DockerImage: req.DockerImage,
	}); err != nil {
		return nil, refusedToStatus(err)
	}
	return &proto.GameAck{}, nil
}

func (s *provisionerGRPCServer) ListGameActivity(ctx context.Context, req *proto.GameActivityRequest) (*proto.GameActivityResponse, error) {
	m, err := s.game()
	if err != nil {
		return nil, err
	}
	page, err := m.GameActivity(ctx, gameRefFromProto(req.Ref), int(req.Page))
	if err != nil {
		return nil, refusedToStatus(err)
	}
	out := make([]*proto.GameActivity, len(page.Entries))
	for i, e := range page.Entries {
		out[i] = &proto.GameActivity{
			Id: e.ID, Event: e.Event, Description: e.Description, Actor: e.Actor, Ip: e.IP,
			Api: e.API, Properties: e.Properties, TimestampUnix: e.TimestampUnix,
		}
	}
	return &proto.GameActivityResponse{Entries: out, Page: int32(page.Page), TotalPages: int32(page.TotalPages)}, nil
}

// --- gRPC client adapter (host side) ---
// The client side always exposes GameServerManager; the host only routes
// game calls to plugins whose Describe declared the capability.

func (c *provisionerGRPCClient) GameServer(ctx context.Context, ref GameRef) (GameServer, error) {
	resp, err := c.client.GetGameServer(ctx, &proto.GameServerRequest{Ref: gameRefToProto(ref)})
	if err != nil {
		return GameServer{}, refusedFromStatus(err)
	}
	g := GameServer{
		Name: resp.Name, Description: resp.Description, Status: resp.Status, Address: resp.Address,
		SFTPHost: resp.SftpHost, SFTPPort: int(resp.SftpPort), SFTPUsername: resp.SftpUsername,
		Features: resp.Features, DockerImage: resp.DockerImage, Invocation: resp.Invocation,
		PanelURL: resp.PanelUrl, Details: resp.Details,
	}
	if l := resp.Limits; l != nil {
		g.Limits = GameLimits{
			MemoryBytes: l.MemoryBytes, SwapBytes: l.SwapBytes, DiskBytes: l.DiskBytes, CPUPct: l.CpuPct,
			IOWeight: l.IoWeight, Threads: l.Threads, OOMKiller: l.OomKiller,
		}
	}
	if f := resp.FeatureLimits; f != nil {
		g.FeatureLimits = GameFeatureLimits{Databases: int(f.Databases), Backups: int(f.Backups), Allocations: int(f.Allocations)}
	}
	return g, nil
}

func (c *provisionerGRPCClient) GameConsole(ctx context.Context, ref GameRef) (GameConsole, error) {
	resp, err := c.client.GetGameConsole(ctx, &proto.GameServerRequest{Ref: gameRefToProto(ref)})
	if err != nil {
		return GameConsole{}, refusedFromStatus(err)
	}
	return GameConsole{
		Protocol: resp.Protocol, WebsocketURL: resp.WebsocketUrl, Headers: resp.Headers, Token: resp.Token,
		ExpiresUnix: resp.ExpiresUnix, CAPEM: resp.CaPem, InsecureSkipVerify: resp.InsecureSkipVerify,
	}, nil
}

func (c *provisionerGRPCClient) GameCommand(ctx context.Context, ref GameRef, command string) error {
	_, err := c.client.SendGameCommand(ctx, &proto.GameCommandRequest{Ref: gameRefToProto(ref), Command: command})
	return refusedFromStatus(err)
}

func (c *provisionerGRPCClient) GameFiles(ctx context.Context, ref GameRef, directory string) ([]GameFile, error) {
	resp, err := c.client.ListGameFiles(ctx, &proto.GameFilesRequest{Ref: gameRefToProto(ref), Directory: directory})
	if err != nil {
		return nil, refusedFromStatus(err)
	}
	out := make([]GameFile, len(resp.Files))
	for i, f := range resp.Files {
		out[i] = gameFileFromProto(f)
	}
	return out, nil
}

func (c *provisionerGRPCClient) GameFileRead(ctx context.Context, ref GameRef, path string) ([]byte, error) {
	resp, err := c.client.ReadGameFile(ctx, &proto.GameFileReadRequest{Ref: gameRefToProto(ref), Path: path})
	if err != nil {
		return nil, refusedFromStatus(err)
	}
	return resp.Content, nil
}

func (c *provisionerGRPCClient) GameFileWrite(ctx context.Context, ref GameRef, path string, content []byte) error {
	_, err := c.client.WriteGameFile(ctx, &proto.GameFileWriteRequest{Ref: gameRefToProto(ref), Path: path, Content: content})
	return refusedFromStatus(err)
}

func (c *provisionerGRPCClient) GameFileAction(ctx context.Context, ref GameRef, in GameFileAction) (GameFileActionResult, error) {
	renames := make([]*proto.GameRename, len(in.Renames))
	for i, r := range in.Renames {
		renames[i] = &proto.GameRename{From: r.From, To: r.To}
	}
	resp, err := c.client.GameFileAction(ctx, &proto.GameFileActionRequest{
		Ref: gameRefToProto(ref), Action: in.Action, Root: in.Root, Files: in.Files, Renames: renames,
		Name: in.Name, Mode: in.Mode, Url: in.URL,
	})
	if err != nil {
		return GameFileActionResult{}, refusedFromStatus(err)
	}
	res := GameFileActionResult{URL: resp.Url}
	if resp.File != nil {
		f := gameFileFromProto(resp.File)
		res.File = &f
	}
	return res, nil
}

func (c *provisionerGRPCClient) GameBackups(ctx context.Context, ref GameRef) (GameBackupList, error) {
	resp, err := c.client.ListGameBackups(ctx, &proto.GameServerRequest{Ref: gameRefToProto(ref)})
	if err != nil {
		return GameBackupList{}, refusedFromStatus(err)
	}
	out := GameBackupList{Backups: make([]GameBackup, len(resp.Backups)), Limit: int(resp.Limit)}
	for i, b := range resp.Backups {
		out.Backups[i] = gameBackupFromProto(b)
	}
	return out, nil
}

func (c *provisionerGRPCClient) GameBackupAction(ctx context.Context, ref GameRef, in GameBackupAction) (GameBackupActionResult, error) {
	resp, err := c.client.GameBackupAction(ctx, &proto.GameBackupActionRequest{
		Ref: gameRefToProto(ref), Action: in.Action, Id: in.ID, Name: in.Name, Ignored: in.Ignored,
		Locked: in.Locked, Truncate: in.Truncate,
	})
	if err != nil {
		return GameBackupActionResult{}, refusedFromStatus(err)
	}
	res := GameBackupActionResult{URL: resp.Url}
	if resp.Backup != nil {
		b := gameBackupFromProto(resp.Backup)
		res.Backup = &b
	}
	return res, nil
}

func (c *provisionerGRPCClient) GameDatabases(ctx context.Context, ref GameRef) (GameDatabaseList, error) {
	resp, err := c.client.ListGameDatabases(ctx, &proto.GameServerRequest{Ref: gameRefToProto(ref)})
	if err != nil {
		return GameDatabaseList{}, refusedFromStatus(err)
	}
	out := GameDatabaseList{Databases: make([]GameDatabase, len(resp.Databases)), Limit: int(resp.Limit)}
	for i, d := range resp.Databases {
		out.Databases[i] = gameDatabaseFromProto(d)
	}
	return out, nil
}

func (c *provisionerGRPCClient) GameDatabaseAction(ctx context.Context, ref GameRef, in GameDatabaseAction) (*GameDatabase, error) {
	resp, err := c.client.GameDatabaseAction(ctx, &proto.GameDatabaseActionRequest{
		Ref: gameRefToProto(ref), Action: in.Action, Id: in.ID, Name: in.Name, Remote: in.Remote,
	})
	if err != nil {
		return nil, refusedFromStatus(err)
	}
	if resp.Database == nil {
		return nil, nil
	}
	d := gameDatabaseFromProto(resp.Database)
	return &d, nil
}

func (c *provisionerGRPCClient) GameSchedules(ctx context.Context, ref GameRef) ([]GameSchedule, error) {
	resp, err := c.client.ListGameSchedules(ctx, &proto.GameServerRequest{Ref: gameRefToProto(ref)})
	if err != nil {
		return nil, refusedFromStatus(err)
	}
	out := make([]GameSchedule, len(resp.Schedules))
	for i, s := range resp.Schedules {
		out[i] = gameScheduleFromProto(s)
	}
	return out, nil
}

func (c *provisionerGRPCClient) GameScheduleAction(ctx context.Context, ref GameRef, in GameScheduleAction) (*GameSchedule, error) {
	resp, err := c.client.GameScheduleAction(ctx, &proto.GameScheduleActionRequest{
		Ref: gameRefToProto(ref), Action: in.Action, Id: in.ID, Name: in.Name, Cron: gameCronToProto(in.Cron),
		Active: in.Active, OnlyWhenOnline: in.OnlyWhenOnline,
	})
	if err != nil {
		return nil, refusedFromStatus(err)
	}
	if resp.Schedule == nil {
		return nil, nil
	}
	s := gameScheduleFromProto(resp.Schedule)
	return &s, nil
}

func (c *provisionerGRPCClient) GameTaskAction(ctx context.Context, ref GameRef, in GameTaskAction) (*GameTask, error) {
	resp, err := c.client.GameTaskAction(ctx, &proto.GameTaskActionRequest{
		Ref: gameRefToProto(ref), Action: in.Action, ScheduleId: in.ScheduleID, Id: in.ID,
		TaskAction: in.TaskAction, Payload: in.Payload, TimeOffset: int32(in.TimeOffset),
		ContinueOnFailure: in.ContinueOnFailure,
	})
	if err != nil {
		return nil, refusedFromStatus(err)
	}
	if resp.Task == nil {
		return nil, nil
	}
	t := gameTaskFromProto(resp.Task)
	return &t, nil
}

func (c *provisionerGRPCClient) GameAllocations(ctx context.Context, ref GameRef) (GameAllocationList, error) {
	resp, err := c.client.ListGameAllocations(ctx, &proto.GameServerRequest{Ref: gameRefToProto(ref)})
	if err != nil {
		return GameAllocationList{}, refusedFromStatus(err)
	}
	out := GameAllocationList{Allocations: make([]GameAllocation, len(resp.Allocations)), Limit: int(resp.Limit)}
	for i, a := range resp.Allocations {
		out.Allocations[i] = gameAllocationFromProto(a)
	}
	return out, nil
}

func (c *provisionerGRPCClient) GameAllocationAction(ctx context.Context, ref GameRef, in GameAllocationAction) (*GameAllocation, error) {
	resp, err := c.client.GameAllocationAction(ctx, &proto.GameAllocationActionRequest{
		Ref: gameRefToProto(ref), Action: in.Action, Id: in.ID, Notes: in.Notes,
	})
	if err != nil {
		return nil, refusedFromStatus(err)
	}
	if resp.Allocation == nil {
		return nil, nil
	}
	a := gameAllocationFromProto(resp.Allocation)
	return &a, nil
}

func (c *provisionerGRPCClient) GameUsers(ctx context.Context, ref GameRef) (GameUserList, error) {
	resp, err := c.client.ListGameUsers(ctx, &proto.GameServerRequest{Ref: gameRefToProto(ref)})
	if err != nil {
		return GameUserList{}, refusedFromStatus(err)
	}
	out := GameUserList{
		Users:   make([]GameUser, len(resp.Users)),
		Catalog: make([]GamePermissionGroup, len(resp.Catalog)),
	}
	for i, u := range resp.Users {
		out.Users[i] = gameUserFromProto(u)
	}
	for i, g := range resp.Catalog {
		perms := make([]GamePermission, len(g.Permissions))
		for j, p := range g.Permissions {
			perms[j] = GamePermission{Key: p.Key, Description: p.Description}
		}
		out.Catalog[i] = GamePermissionGroup{Key: g.Key, Description: g.Description, Permissions: perms}
	}
	return out, nil
}

func (c *provisionerGRPCClient) GameUserAction(ctx context.Context, ref GameRef, in GameUserAction) (*GameUser, error) {
	resp, err := c.client.GameUserAction(ctx, &proto.GameUserActionRequest{
		Ref: gameRefToProto(ref), Action: in.Action, Id: in.ID, Email: in.Email, Permissions: in.Permissions,
	})
	if err != nil {
		return nil, refusedFromStatus(err)
	}
	if resp.User == nil {
		return nil, nil
	}
	u := gameUserFromProto(resp.User)
	return &u, nil
}

func (c *provisionerGRPCClient) GameStartup(ctx context.Context, ref GameRef) (GameStartup, error) {
	resp, err := c.client.GetGameStartup(ctx, &proto.GameServerRequest{Ref: gameRefToProto(ref)})
	if err != nil {
		return GameStartup{}, refusedFromStatus(err)
	}
	out := GameStartup{
		Command: resp.Command, RawCommand: resp.RawCommand, DockerImage: resp.DockerImage,
		DockerImages: resp.DockerImages, Variables: make([]GameVariable, len(resp.Variables)),
	}
	for i, v := range resp.Variables {
		out.Variables[i] = gameVariableFromProto(v)
	}
	return out, nil
}

func (c *provisionerGRPCClient) GameVariableSet(ctx context.Context, ref GameRef, key, value string) (GameVariable, error) {
	resp, err := c.client.SetGameVariable(ctx, &proto.GameVariableSetRequest{Ref: gameRefToProto(ref), Key: key, Value: value})
	if err != nil {
		return GameVariable{}, refusedFromStatus(err)
	}
	return gameVariableFromProto(resp.Variable), nil
}

func (c *provisionerGRPCClient) GameSettings(ctx context.Context, ref GameRef, in GameSettingsAction) error {
	_, err := c.client.UpdateGameSettings(ctx, &proto.GameSettingsRequest{
		Ref: gameRefToProto(ref), Action: in.Action, Name: in.Name, Description: in.Description, DockerImage: in.DockerImage,
	})
	return refusedFromStatus(err)
}

func (c *provisionerGRPCClient) GameActivity(ctx context.Context, ref GameRef, page int) (GameActivityPage, error) {
	resp, err := c.client.ListGameActivity(ctx, &proto.GameActivityRequest{Ref: gameRefToProto(ref), Page: int32(page)})
	if err != nil {
		return GameActivityPage{}, refusedFromStatus(err)
	}
	out := GameActivityPage{Entries: make([]GameActivity, len(resp.Entries)), Page: int(resp.Page), TotalPages: int(resp.TotalPages)}
	for i, e := range resp.Entries {
		out.Entries[i] = GameActivity{
			ID: e.Id, Event: e.Event, Description: e.Description, Actor: e.Actor, IP: e.Ip,
			API: e.Api, Properties: e.Properties, TimestampUnix: e.TimestampUnix,
		}
	}
	return out, nil
}
