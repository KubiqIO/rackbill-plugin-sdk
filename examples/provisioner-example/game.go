package main

import (
	"context"

	sdk "rackbill.com/sdk/plugin"
)

// GameServerManager is optional. Enable SupportsGameServer only after replacing
// these stubs with provider calls, enabling SupportsVM, and implementing VMManager
// for the game server's power and state operations. Keep requests scoped to ref's
// tenant and server, honor ctx, and use sdk.Refused for public provider refusals.
var _ sdk.GameServerManager = exampleProvisioner{}

// GameServer reads the server's display information and resource limits. Include
// only usable sdk.GameFeature* values in Features so unavailable tabs stay hidden.
func (exampleProvisioner) GameServer(ctx context.Context, ref sdk.GameRef) (sdk.GameServer, error) {
	return sdk.GameServer{}, unimplemented(ctx, "GameServer")
}

// GameConsole obtains the supported websocket protocol, endpoint, and temporary
// credentials for this server. Keep console tokens and provider credentials out
// of logs; return an expiry when the provider requires credentials to be renewed.
func (exampleProvisioner) GameConsole(ctx context.Context, ref sdk.GameRef) (sdk.GameConsole, error) {
	return sdk.GameConsole{}, unimplemented(ctx, "GameConsole")
}

// GameCommand sends one command to this game server through the provider API.
// Preserve the command text and report a provider refusal without claiming success.
func (exampleProvisioner) GameCommand(ctx context.Context, ref sdk.GameRef, command string) error {
	return unimplemented(ctx, "GameCommand")
}

// GameFiles lists one directory inside this server's permitted filesystem.
// Return file metadata and prevent traversal or symlinks from escaping that scope.
func (exampleProvisioner) GameFiles(ctx context.Context, ref sdk.GameRef, directory string) ([]sdk.GameFile, error) {
	return nil, unimplemented(ctx, "GameFiles")
}

// GameFileRead retrieves a file's bytes from this server's filesystem. Enforce
// provider size and path restrictions and keep file contents out of request logs.
func (exampleProvisioner) GameFileRead(ctx context.Context, ref sdk.GameRef, path string) ([]byte, error) {
	return nil, unimplemented(ctx, "GameFileRead")
}

// GameFileWrite saves the supplied bytes to a permitted file on this server.
// Validate the path, honor provider write limits, and return any rejected write.
func (exampleProvisioner) GameFileWrite(ctx context.Context, ref sdk.GameRef, path string, content []byte) error {
	return unimplemented(ctx, "GameFileWrite")
}

// GameFileAction maps the requested sdk.GameFile* action to its provider call.
// Use only the action's relevant input fields; signed upload or download URLs
// belong in the result, and all referenced paths must remain inside this server.
func (exampleProvisioner) GameFileAction(ctx context.Context, ref sdk.GameRef, in sdk.GameFileAction) (sdk.GameFileActionResult, error) {
	return sdk.GameFileActionResult{}, unimplemented(ctx, "GameFileAction")
}

// GameBackups lists this server's provider-managed backups and its backup limit.
// Preserve completion, success, and lock status so customers see usable actions.
func (exampleProvisioner) GameBackups(ctx context.Context, ref sdk.GameRef) (sdk.GameBackupList, error) {
	return sdk.GameBackupList{}, unimplemented(ctx, "GameBackups")
}

// GameBackupAction performs the requested sdk.GameBackup* operation on a backup
// owned by this server. Respect backup limits and locks, and return signed download
// URLs only when requested by the corresponding action.
func (exampleProvisioner) GameBackupAction(ctx context.Context, ref sdk.GameRef, in sdk.GameBackupAction) (sdk.GameBackupActionResult, error) {
	return sdk.GameBackupActionResult{}, unimplemented(ctx, "GameBackupAction")
}

// GameDatabases lists databases belonging to this server and the provider's limit.
// Return only this server's connection details and never log database passwords.
func (exampleProvisioner) GameDatabases(ctx context.Context, ref sdk.GameRef) (sdk.GameDatabaseList, error) {
	return sdk.GameDatabaseList{}, unimplemented(ctx, "GameDatabases")
}

// GameDatabaseAction creates, rotates credentials for, or deletes one of this
// server's databases. Enforce provider limits and ownership before acting, and
// return the resulting database when the action produces one.
func (exampleProvisioner) GameDatabaseAction(ctx context.Context, ref sdk.GameRef, in sdk.GameDatabaseAction) (*sdk.GameDatabase, error) {
	return nil, unimplemented(ctx, "GameDatabaseAction")
}

// GameSchedules reads this server's schedules, including their tasks and current
// execution state. Report the provider's cron fields and timestamps accurately.
func (exampleProvisioner) GameSchedules(ctx context.Context, ref sdk.GameRef) ([]sdk.GameSchedule, error) {
	return nil, unimplemented(ctx, "GameSchedules")
}

// GameScheduleAction saves, deletes, or executes a schedule owned by this server.
// Validate cron fields and provider restrictions; return the saved schedule when
// appropriate instead of inventing a successful execution result.
func (exampleProvisioner) GameScheduleAction(ctx context.Context, ref sdk.GameRef, in sdk.GameScheduleAction) (*sdk.GameSchedule, error) {
	return nil, unimplemented(ctx, "GameScheduleAction")
}

// GameTaskAction saves or deletes a task within the named schedule. Verify that
// the schedule and task belong to this server, and preserve ordering, delay, and
// continue-on-failure settings when translating the provider request.
func (exampleProvisioner) GameTaskAction(ctx context.Context, ref sdk.GameRef, in sdk.GameTaskAction) (*sdk.GameTask, error) {
	return nil, unimplemented(ctx, "GameTaskAction")
}

// GameAllocations lists this server's assigned addresses and ports together with
// its allocation limit. Preserve the provider's primary allocation designation.
func (exampleProvisioner) GameAllocations(ctx context.Context, ref sdk.GameRef) (sdk.GameAllocationList, error) {
	return sdk.GameAllocationList{}, unimplemented(ctx, "GameAllocations")
}

// GameAllocationAction adds, annotates, promotes, or removes an allocation for
// this server. Respect provider limits and refuse changes to another server's
// allocation or a primary allocation that the provider does not allow removing.
func (exampleProvisioner) GameAllocationAction(ctx context.Context, ref sdk.GameRef, in sdk.GameAllocationAction) (*sdk.GameAllocation, error) {
	return nil, unimplemented(ctx, "GameAllocationAction")
}

// GameUsers reads this server's delegated users and the provider's permission
// catalog. Include only users the current server owner is allowed to manage.
func (exampleProvisioner) GameUsers(ctx context.Context, ref sdk.GameRef) (sdk.GameUserList, error) {
	return sdk.GameUserList{}, unimplemented(ctx, "GameUsers")
}

// GameUserAction saves or removes a delegated user for this server. Validate the
// requested permissions against the provider's catalog and delegation rules.
func (exampleProvisioner) GameUserAction(ctx context.Context, ref sdk.GameRef, in sdk.GameUserAction) (*sdk.GameUser, error) {
	return nil, unimplemented(ctx, "GameUserAction")
}

// GameStartup reads the startup command, variables, and permitted container
// images. Preserve each variable's editability and validation rules for the UI.
func (exampleProvisioner) GameStartup(ctx context.Context, ref sdk.GameRef) (sdk.GameStartup, error) {
	return sdk.GameStartup{}, unimplemented(ctx, "GameStartup")
}

// GameVariableSet changes one editable startup variable and returns its updated
// description. Enforce the provider's validation rules and do not log its value.
func (exampleProvisioner) GameVariableSet(ctx context.Context, ref sdk.GameRef, key, value string) (sdk.GameVariable, error) {
	return sdk.GameVariable{}, unimplemented(ctx, "GameVariableSet")
}

// GameSettings applies the requested rename, reinstall, or container-image
// operation to this server. Validate the action and allowed image; perform a
// destructive reinstall only when that explicit action was requested.
func (exampleProvisioner) GameSettings(ctx context.Context, ref sdk.GameRef, in sdk.GameSettingsAction) error {
	return unimplemented(ctx, "GameSettings")
}

// GameActivity reads one page of this server's activity and returns pagination
// information. Include public descriptions while withholding unrelated activity,
// credentials, and provider details that customers should not see.
func (exampleProvisioner) GameActivity(ctx context.Context, ref sdk.GameRef, page int) (sdk.GameActivityPage, error) {
	return sdk.GameActivityPage{}, unimplemented(ctx, "GameActivity")
}
