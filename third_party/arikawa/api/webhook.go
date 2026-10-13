package api

import (
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/utils/httputil"
	"github.com/ayn2op/arikawa/v3/utils/json/option"
)

var EndpointWebhooks = Endpoint + "webhooks/"

// https://discord.com/developers/docs/resources/webhook#create-webhook-json-params
type CreateWebhookData struct {
	// Name is the name of the webhook (1-80 characters).
	Name string `json:"name"`
	// Avatar is the image for the default webhook avatar.
	Avatar *Image `json:"avatar"`
}

// CreateWebhook creates a new webhook.
//
// Webhooks cannot be named "clyde".
//
// Requires the MANAGE_WEBHOOKS permission.
func (c *Client) CreateWebhook(
	channelID discord.ChannelID, data CreateWebhookData) (*discord.Webhook, error) {
	return c.RequestJSON[*discord.Webhook](
		"POST",
		EndpointChannels+channelID.String()+"/webhooks",
		httputil.WithJSONBody(data),
	)
}

// ChannelWebhooks returns the webhooks of the channel with the given ID.
//
// Requires the MANAGE_WEBHOOKS permission.
func (c *Client) ChannelWebhooks(channelID discord.ChannelID) ([]discord.Webhook, error) {
	return c.RequestJSON[[]discord.Webhook]("GET", EndpointChannels+channelID.String()+"/webhooks")
}

// GuildWebhooks returns the webhooks of the guild with the given ID.
//
// Requires the MANAGE_WEBHOOKS permission.
func (c *Client) GuildWebhooks(guildID discord.GuildID) ([]discord.Webhook, error) {
	return c.RequestJSON[[]discord.Webhook]("GET", EndpointGuilds+guildID.String()+"/webhooks")
}

// Webhook returns the webhook with the given id.
func (c *Client) Webhook(webhookID discord.WebhookID) (*discord.Webhook, error) {
	return c.RequestJSON[*discord.Webhook]("GET", EndpointWebhooks+webhookID.String())
}

// https://discord.com/developers/docs/resources/webhook#modify-webhook-json-params
type ModifyWebhookData struct {
	// Name is the default name of the webhook.
	Name option.Option[string] `json:"name,omitempty"`
	// Avatar is the image for the default webhook avatar.
	Avatar *Image `json:"avatar,omitempty"`
	// ChannelID is the new channel id this webhook should be moved to.
	ChannelID discord.ChannelID `json:"channel_id,omitempty"`
}

// ModifyWebhook modifies a webhook.
//
// Requires the MANAGE_WEBHOOKS permission.
func (c *Client) ModifyWebhook(
	webhookID discord.WebhookID, data ModifyWebhookData) (*discord.Webhook, error) {
	return c.RequestJSON[*discord.Webhook](
		"PATCH",
		EndpointWebhooks+webhookID.String(),
		httputil.WithJSONBody(data),
	)
}

// DeleteWebhook deletes a webhook permanently.
//
// Requires the MANAGE_WEBHOOKS permission.
func (c *Client) DeleteWebhook(webhookID discord.WebhookID) error {
	return c.FastRequest("DELETE", EndpointWebhooks+webhookID.String())
}
