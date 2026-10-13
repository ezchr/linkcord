package api

import (
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/utils/httputil"
	"github.com/ayn2op/arikawa/v3/utils/json/option"
)

// ListAutoModerationRules gets a list of all rules currently configured for the guild. Returns a list of auto moderation rule objects for the given guild.
//
// This endpoint requires the MANAGE_GUILD permission.
func (c *Client) ListAutoModerationRules(guildID discord.GuildID) ([]discord.AutoModerationRule, error) {
	return c.RequestJSON[[]discord.AutoModerationRule](
		"GET",
		EndpointGuilds+guildID.String()+"/auto-moderation/rules",
	)
}

// GetAutoModerationRule gets a single rule. Returns an auto moderation rule object.
//
// This endpoint requires the MANAGE_GUILD permission.
func (c *Client) GetAutoModerationRule(guildID discord.GuildID, ruleID discord.AutoModerationRuleID) (discord.AutoModerationRule, error) {
	return c.RequestJSON[discord.AutoModerationRule](
		"GET",
		EndpointGuilds+guildID.String()+"/auto-moderation/rules/"+ruleID.String(),
	)
}

// CreateAutoModerationRule creates a new rule. Returns an auto moderation rule on success. Fires an Auto Moderation Rule Create Gateway event.
//
// This endpoint requires the MANAGE_GUILD permission.
//
// This endpoint supports the X-Audit-Log-Reason header.
func (c *Client) CreateAutoModerationRule(guildID discord.GuildID, rule discord.AutoModerationRule) (*discord.AutoModerationRule, error) {
	return c.RequestJSON[*discord.AutoModerationRule]("POST", EndpointGuilds+guildID.String()+"/auto-moderation/rules",
		httputil.WithJSONBody(rule),
	)
}

type ModifyAutoModerationRuleData struct {
	// the rule name
	Name option.Option[string] `json:"name,omitempty"`
	// the event type
	EventType option.Option[discord.AutoModerationEventType] `json:"event_type,omitempty"`
	// the trigger metadata
	TriggerMetadata option.Option[discord.AutoModerationTriggerMetadata] `json:"triggr_metadata,omitempty"`
	// the actions which will execute when the rule is triggered
	Actions option.Option[[]discord.AutoModerationAction] `json:"actions,omitempty"`
	// whether the rule is enabled
	Enabled option.Option[bool] `json:"enabled,omitempty"`
	// the role ids that should not be affected by the rule (Maximum of 20)
	ExemptRules option.Option[[]discord.RoleID] `json:"exempt_roles,omitempty"`
	// the channel ids that should not be affected by the rule (Maximum of 50)
	ExemptChannels option.Option[[]discord.ChannelID] `json:"exempt_channels,omitempty"`
	AuditLogReason
}

// ModifyAutoModerationRule modifies an existing rule. Returns an auto moderation rule on success. Fires an Auto Moderation Rule Update Gateway event.
//
// Requires MANAGE_GUILD permissions.
//
// All parameters for this endpoint are optional.
//
// This endpoint supports the X-Audit-Log-Reason header.
func (c *Client) ModifyAutoModerationRule(guildID discord.GuildID, ruleID discord.AutoModerationRuleID, data ModifyAutoModerationRuleData) (*discord.AutoModerationRule, error) {
	return c.RequestJSON[*discord.AutoModerationRule]("PATCH", EndpointGuilds+guildID.String()+"/auto-moderation/rules/"+ruleID.String(),
		httputil.WithJSONBody(data),
		httputil.WithHeaders(data.Header()),
	)
}

type DeleteAutoModerationRuleData struct {
	AuditLogReason `json:"-"`
}

// DeleteAutoModerationRule deletes a rule. Returns a 204 on success. Fires an Auto Moderation Rule Delete Gateway event.
//
// This endpoint requires the MANAGE_GUILD permission.
//
// This endpoint supports the X-Audit-Log-Reason header.
func (c *Client) DeleteAutoModerationRule(guildID discord.GuildID, ruleID discord.AutoModerationRuleID, data DeleteAutoModerationRuleData) error {
	return c.FastRequest("DELETE", EndpointGuilds+guildID.String()+"/auto-moderation/rules/"+ruleID.String(), httputil.WithHeaders(data.Header()))
}
