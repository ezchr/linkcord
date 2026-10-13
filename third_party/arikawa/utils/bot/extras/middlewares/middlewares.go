package middlewares

import (
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/utils/bot"
	"github.com/ayn2op/arikawa/v3/utils/bot/extras/infer"
)

func AdminOnly(ctx *bot.Context) func(any) error {
	return func(ev any) error {
		var channelID = infer.ChannelID(ev)
		if !channelID.IsValid() {
			return bot.ErrBreak
		}

		var userID = infer.UserID(ev)
		if !userID.IsValid() {
			return bot.ErrBreak
		}

		p, err := ctx.Permissions(channelID, userID)
		if err == nil && p.Has(discord.PermissionAdministrator) {
			return nil
		}

		return bot.ErrBreak
	}
}

func GuildOnly(ctx *bot.Context) func(any) error {
	return func(ev any) error {
		// Try and infer the GuildID.
		if guildID := infer.GuildID(ev); guildID.IsValid() {
			return nil
		}

		var channelID = infer.ChannelID(ev)
		if !channelID.IsValid() {
			return bot.ErrBreak
		}

		c, err := ctx.Channel(channelID)
		if err != nil || !c.GuildID.IsValid() {
			return bot.ErrBreak
		}

		return nil
	}
}
