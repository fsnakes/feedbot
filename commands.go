package feedbot

import (
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
)

type context struct {
	bot  *Bot
	s    *discordgo.Session
	m    *discordgo.MessageCreate
	args []string
}
type commandHandler = func(c *context) error

var mentionPrefix = "<@0>"
var mentionPrefixLen = len(mentionPrefix)
var prefix = "/feed:"
var prefixLen = len(prefix)

var mux = map[string]commandHandler{
	"help":   nil,
	"add":    nil,
	"remove": nil,
	"list":   nil,
	"set":    nil,
}

// onReady handles the Discord READY event
func (bot *Bot) onReady(s *discordgo.Session, m *discordgo.Ready) {
	mentionPrefix = m.User.Mention()
	mentionPrefixLen = len(mentionPrefix)
}

// onMessageCreate handles the Discord MESSAGE_CREATE event
func (bot *Bot) onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.Bot {
		return
	}

	var content string
	if strings.HasPrefix(m.Content, mentionPrefix) {
		content = content[mentionPrefixLen:]
	} else if strings.HasPrefix(m.Content, prefix) {
		content = content[prefixLen:]
	} else {
		return
	}

	parts := strings.Split(content, " ")
	if len(parts) < 1 {
		return
	}
	f, ok := mux[parts[0]]
	if !ok {
		return
	}

	var args []string
	if len(parts) > 1 {
		args = parts[1:]
	}

	defer func() {
		if err := recover(); err != nil {
			l.Println(fmt.Sprintf("cmd:%s pnc:%v", parts[0], err))
		}
	}()

	ctx := &context{
		bot:  bot,
		s:    s,
		m:    m,
		args: args,
	}
	err := f(ctx)
	if err != nil {
		l.Println(fmt.Sprintf("cmd:%s err:%v", parts[0], err))
	}
}

const helpText = `
**feedbot**

**commands:**
- help: print this message
- add <uri> [channel]: add an RSS feed by its URI; optionally specifying a channel where updates will be posted
- remove <id>: remove an RSS feed by its ID (see the list command)
- list: list the RSS feeds active in this guild, and any additional configuration options
- set channel <id> [channel]: set the channel a given feed should write to; will assume current channel if unspecified
- set contact <user|channel>: set the emergency contact for this guild; defaults to the server owner
- set embed <on|off> [id]: enable or disable embeds for this guild; optionally specifying a feed to change this behavior for
- set webhook <on|off> [id]: enable or disable webhooks for this guild, optionally specifying a feed to change this behavior for

**how it works:**
every 60 minutes, feedbot will ping the feeds its users have specified. for feeds that have new content, feedbot
will find every discord channel with a subscription, and send an update.

**permissions:**
feedbot will only respect users who poesess the **ADMINISTRATOR** permission in a guild.discordgo

feedbot by default only requires **READ MESSAGES** and **SEND MESSAGES**.

if embeds are enabled for a feed, the **EMBED LINKS** permission must be given.
if webhooks are enabled for a feed, the **MANAGE WEBHOOKS** permission must be given.

**emergency contact:**
if a permission is missing, or a feed is broken, feedbot will notify the emergency contact.
`

func help(ctx *context) error {
	_, err := ctx.s.ChannelMessageSend(ctx.m.ChannelID, helpText)
	return err
}

func add(ctx *context) error {
	ok, err := checkPrivilege(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}

	return nil
}

func remove(ctx *context) error {
	ok, err := checkPrivilege(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}

	return nil
}

func list(ctx *context) error {
	ok, err := checkPrivilege(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}

	return nil
}

func set(ctx *context) error {
	ok, err := checkPrivilege(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}

	return nil
}

const adminOnly = "Sorry, feedbot requires the **ADMINISTRATOR** privilege!"

func checkPrivilege(ctx *context) (bool, error) {
	ok, err := memberHasPermission(ctx.s, ctx.m.GuildID, ctx.m.Author.ID, discordgo.PermissionAdministrator)
	if err != nil {
		return false, err
	}
	if !ok {
		if _, err = ctx.s.ChannelMessageSend(ctx.m.ChannelID, adminOnly); err != nil {
			return false, err
		}
	}
	return true, nil
}

func memberHasPermission(s *discordgo.Session, guildID string, userID string, permission int) (bool, error) {
	member, err := s.State.Member(guildID, userID)
	if err != nil {
		if member, err = s.GuildMember(guildID, userID); err != nil {
			return false, err
		}
	}

	// Iterate through the role IDs stored in member.Roles
	// to check permissions
	for _, roleID := range member.Roles {
		role, err := s.State.Role(guildID, roleID)
		if err != nil {
			return false, err
		}
		if role.Permissions&permission != 0 {
			return true, nil
		}
	}

	return false, nil
}