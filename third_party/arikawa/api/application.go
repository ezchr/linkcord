package api

import (
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/utils/httputil"
	"github.com/ayn2op/arikawa/v3/utils/json"
)

var EndpointApplications = Endpoint + "applications/"

// CurrentApplication returns the current bot account's Discord application. It
// can be used to get the application ID.
func (c *Client) CurrentApplication() (*discord.Application, error) {
	return c.RequestJSON[*discord.Application](
		"GET",
		Endpoint+"/oauth2/applications/@me",
	)
}

// https://discord.com/developers/docs/interactions/application-commands#create-global-application-command
// https://discord.com/developers/docs/interactions/application-commands#bulk-overwrite-guild-application-commands
type CreateCommandData struct {
	ID                       discord.CommandID      `json:"id,omitempty"`
	Name                     string                 `json:"name"`
	NameLocalizations        discord.StringLocales  `json:"name_localizations,omitempty"`
	Description              string                 `json:"description"`
	DescriptionLocalizations discord.StringLocales  `json:"description_localizations,omitempty"`
	Options                  discord.CommandOptions `json:"options,omitempty"`
	DefaultMemberPermissions *discord.Permissions   `json:"default_member_permissions,string,omitempty"`
	NoDMPermission           bool                   `json:"-"`
	NoDefaultPermission      bool                   `json:"-"`
	Type                     discord.CommandType    `json:"type,omitempty"`
}

func (c CreateCommandData) MarshalJSON() ([]byte, error) {
	type RawCreateCommandData CreateCommandData
	cmd := struct {
		RawCreateCommandData
		DMPermission      bool `json:"dm_permission"`
		DefaultPermission bool `json:"default_permission"`
	}{
		RawCreateCommandData: (RawCreateCommandData)(c),
		// Discord defaults default_permission to true, so we need to invert the
		// meaning of the field (>No<DefaultPermission) to match Go's default
		// value, false.
		DefaultPermission: !c.NoDefaultPermission,
		DMPermission:      !c.NoDMPermission,
	}
	return json.Marshal(cmd)
}

func (c *CreateCommandData) UnmarshalJSON(data []byte) error {
	type RawCreateCommandData CreateCommandData
	cmd := struct {
		*RawCreateCommandData
		DMPermission      bool `json:"dm_permission"`
		DefaultPermission bool `json:"default_permission"`
	}{RawCreateCommandData: (*RawCreateCommandData)(c)}
	if err := json.Unmarshal(data, &cmd); err != nil {
		return err
	}

	// Discord defaults default_permission to true, so we need to invert the
	// meaning of the field (>No<DefaultPermission) to match Go's default
	// value, false.
	c.NoDefaultPermission = !cmd.DefaultPermission
	c.NoDMPermission = !cmd.DMPermission

	// Discord defaults type to 1 if omitted.
	if c.Type == 0 {
		c.Type = discord.ChatInputCommand
	}

	return nil
}

func (c *Client) Commands(appID discord.AppID) ([]discord.Command, error) {
	return c.RequestJSON[[]discord.Command](
		"GET",
		EndpointApplications+appID.String()+"/commands",
	)
}

func (c *Client) Command(
	appID discord.AppID, commandID discord.CommandID) (*discord.Command, error) {
	return c.RequestJSON[*discord.Command](
		"GET",
		EndpointApplications+appID.String()+"/commands/"+commandID.String(),
	)
}

func (c *Client) CreateCommand(
	appID discord.AppID, data CreateCommandData) (*discord.Command, error) {
	return c.RequestJSON[*discord.Command](
		"POST",
		EndpointApplications+appID.String()+"/commands",
		httputil.WithJSONBody(data),
	)
}

func (c *Client) EditCommand(
	appID discord.AppID,
	commandID discord.CommandID, data CreateCommandData) (*discord.Command, error) {
	return c.RequestJSON[*discord.Command](
		"PATCH",
		EndpointApplications+appID.String()+"/commands/"+commandID.String(),
		httputil.WithJSONBody(data),
	)
}

func (c *Client) DeleteCommand(appID discord.AppID, commandID discord.CommandID) error {
	return c.FastRequest(
		"DELETE",
		EndpointApplications+appID.String()+"/commands/"+commandID.String(),
	)
}

// BulkOverwriteCommands takes a slice of application commands, overwriting
// existing commands that are registered globally for this application. Updates
// will be available in all guilds after 1 hour.
//
// Commands that do not already exist will count toward daily application
// command create limits.
func (c *Client) BulkOverwriteCommands(
	appID discord.AppID, commands []CreateCommandData) ([]discord.Command, error) {
	return c.RequestJSON[[]discord.Command](
		"PUT",
		EndpointApplications+appID.String()+"/commands",
		httputil.WithJSONBody(commands))
}

func (c *Client) GuildCommands(
	appID discord.AppID, guildID discord.GuildID) ([]discord.Command, error) {
	return c.RequestJSON[[]discord.Command](
		"GET",
		EndpointApplications+appID.String()+"/guilds/"+guildID.String()+"/commands",
	)
}

func (c *Client) GuildCommand(
	appID discord.AppID,
	guildID discord.GuildID, commandID discord.CommandID) (*discord.Command, error) {
	return c.RequestJSON[*discord.Command](
		"GET",
		EndpointApplications+appID.String()+
			"/guilds/"+guildID.String()+
			"/commands/"+commandID.String(),
	)
}

func (c *Client) CreateGuildCommand(
	appID discord.AppID,
	guildID discord.GuildID, data CreateCommandData) (*discord.Command, error) {
	return c.RequestJSON[*discord.Command](
		"POST",
		EndpointApplications+appID.String()+"/guilds/"+guildID.String()+"/commands",
		httputil.WithJSONBody(data),
	)
}

func (c *Client) EditGuildCommand(
	appID discord.AppID, guildID discord.GuildID,
	commandID discord.CommandID, data CreateCommandData) (*discord.Command, error) {
	return c.RequestJSON[*discord.Command](
		"PATCH",
		EndpointApplications+appID.String()+
			"/guilds/"+guildID.String()+
			"/commands/"+commandID.String(),
		httputil.WithJSONBody(data),
	)
}

func (c *Client) DeleteGuildCommand(
	appID discord.AppID, guildID discord.GuildID, commandID discord.CommandID) error {

	return c.FastRequest(
		"DELETE",
		EndpointApplications+appID.String()+
			"/guilds/"+guildID.String()+
			"/commands/"+commandID.String(),
	)
}

// BulkOverwriteGuildCommands takes a slice of application commands,
// overwriting existing commands that are registered for the guild.
func (c *Client) BulkOverwriteGuildCommands(
	appID discord.AppID,
	guildID discord.GuildID, commands []CreateCommandData) ([]discord.Command, error) {
	return c.RequestJSON[[]discord.Command](
		"PUT",
		EndpointApplications+appID.String()+"/guilds/"+guildID.String()+"/commands",
		httputil.WithJSONBody(commands))
}

// GuildCommandPermissions fetches command permissions for all commands for the
// application in a guild.
func (c *Client) GuildCommandPermissions(
	appID discord.AppID, guildID discord.GuildID) ([]discord.GuildCommandPermissions, error) {
	return c.RequestJSON[[]discord.GuildCommandPermissions](
		"GET",
		EndpointApplications+appID.String()+"/guilds/"+guildID.String()+"/commands/permissions",
	)
}

// CommandPermissions fetches command permissions for a specific command for
// the application in a guild.
func (c *Client) CommandPermissions(
	appID discord.AppID, guildID discord.GuildID,
	commandID discord.CommandID) (*discord.GuildCommandPermissions, error) {
	return c.RequestJSON[*discord.GuildCommandPermissions](
		"GET",
		EndpointApplications+appID.String()+"/guilds/"+guildID.String()+
			"/commands/"+commandID.String()+"/permissions",
	)
}

type editCommandPermissionsData struct {
	Permissions []discord.CommandPermissions `json:"permissions"`
}

// EditCommandPermissions edits command permissions for a specific command for
// the application in a guild. Up to 10 permission overwrites can be added for
// a command.
//
// Existing permissions for the command will be overwritten in that guild.
// Deleting or renaming a command will permanently delete all permissions for
// that command.
func (c *Client) EditCommandPermissions(
	appID discord.AppID, guildID discord.GuildID, commandID discord.CommandID,
	permissions []discord.CommandPermissions) (*discord.GuildCommandPermissions, error) {

	data := editCommandPermissionsData{Permissions: permissions}

	return c.RequestJSON[*discord.GuildCommandPermissions](
		"PUT",
		EndpointApplications+appID.String()+"/guilds/"+guildID.String()+
			"/commands/"+commandID.String()+"/permissions",
		httputil.WithJSONBody(data),
	)
}

// https://discord.com/developers/docs/interactions/slash-commands#application-command-permissions-object-guild-application-command-permissions-structure
type BatchEditCommandPermissionsData struct {
	ID          discord.CommandID            `json:"id"`
	Permissions []discord.CommandPermissions `json:"permissions"`
}

// BatchEditCommandPermissions batch edits permissions for all commands in a
// guild. Up to 10 permission overwrites can be added for a command.
//
// Existing permissions for the command will be overwritten in that guild.
// Deleting or renaming a command will permanently delete all permissions for
// that command.
func (c *Client) BatchEditCommandPermissions(
	appID discord.AppID, guildID discord.GuildID,
	data []BatchEditCommandPermissionsData) ([]discord.GuildCommandPermissions, error) {
	return c.RequestJSON[[]discord.GuildCommandPermissions](
		"PUT",
		EndpointApplications+appID.String()+"/guilds/"+guildID.String()+"/commands/permissions",
		httputil.WithJSONBody(data),
	)
}
