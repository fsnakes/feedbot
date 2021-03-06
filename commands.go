package feedbot

import (
	"database/sql"
	"fmt"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/pkg/errors"
)

type context struct {
	bot  *Bot
	s    *discordgo.Session
	m    *discordgo.MessageCreate
	args []string
}

// Reply sends a message to the source channel
func (c *context) Reply(m string) error {
	_, err := c.s.ChannelMessageSend(c.m.ChannelID, m)
	return err
}

type commandHandler = func(c *context) error

var mentionPrefix = "<@0>"
var mentionPrefixLen = len(mentionPrefix)
var prefix = "/feed:"
var prefixLen = len(prefix)
var owner = "<@0>"

var channelRegex = regexp.MustCompile(`<#\d+>`)

var mux = map[string]commandHandler{
	"help":        help,
	"add":         add,
	"remove":      remove,
	"list":        list,
	"set":         set,
	"dbg~migrate": dbgMigrate,
}

// onReady handles the Discord READY event
func (bot *Bot) onReady(s *discordgo.Session, m *discordgo.Ready) {
	mentionPrefix = m.User.Mention()
	mentionPrefixLen = len(mentionPrefix)

	apps, err := s.Application("@me")
	if err != nil {
		panic(err)
	}
	owner = apps.Owner.ID
}

// onMessageCreate handles the Discord MESSAGE_CREATE event
func (bot *Bot) onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.Bot {
		return
	}

	var content string
	if strings.HasPrefix(m.Content, mentionPrefix) {
		content = m.Content[mentionPrefixLen:]
	} else if strings.HasPrefix(m.Content, prefix) {
		content = m.Content[prefixLen:]
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
			l.Println(fmt.Sprintf("cmd:%s pnc:%+v", parts[0], err))
			debug.PrintStack()
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
		l.Println(fmt.Sprintf("cmd:%s err:%+v", parts[0], err))
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
- set embed <on|off|inherit> [id]: enable or disable embeds for this guild; optionally specifying a feed to change this behavior for
- set webhook <on|off|inherit> [id]: enable or disable webhooks for this guild, optionally specifying a feed to change this behavior for

the inherit flag may only be used when specifying a feed-specific overwrite!

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

// help
func help(ctx *context) error {
	return ctx.Reply(helpText)
}

// add <uri> [channel]
func add(ctx *context) error {
	ok, err := checkPrivilege(ctx)
	if err != nil {
		return errors.WithStack(err)
	}
	if !ok {
		return nil
	}

	if l := len(ctx.args); l < 1 || l > 2 {
		return ctx.Reply("**usage:** `add <uri> [channel]`; please omit spaces from arguments!")
	}
	uri := ctx.args[0]
	var channel string
	if len(ctx.args) == 2 {
		c := ctx.args[1]
		if !channelRegex.MatchString(c) {
			return ctx.Reply("when specifying a channel ID, please use a #channel mention!")
		}
		// <#...>
		channel = c[2 : len(c)-1]
	} else {
		channel = ctx.m.ChannelID
	}

	feed, err := ctx.bot.c.GetOrCreateFeed(uri)
	if err != nil {
		return err
	}
	sub, err := ctx.bot.c.AddSubscription(channel, ctx.m.GuildID, feed.ID)
	if err == ErrSubExists {
		return ctx.Reply(fmt.Sprintf("this subscription (#%d) already exists!", sub.ID))
	} else if err != nil {
		return err
	}

	return ctx.Reply(fmt.Sprintf("subscription #%d created!", sub.ID))
}

// remove <id>
func remove(ctx *context) error {
	ok, err := checkPrivilege(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}

	if len(ctx.args) != 1 {
		return ctx.Reply("**usage:** `remove <id>`; please omit spaces from arguments?!")
	}
	id, err := strconv.Atoi(ctx.args[0])
	if err != nil {
		return ctx.Reply("`id` must be a number!")
	}
	sub, err := ctx.bot.c.GetSubscription(id)
	if err == sql.ErrNoRows {
		return ctx.Reply("could not find a subscription with that ID, check the list again?")
	} else if err != nil {
		return err
	}

	if sub.GuildID != ctx.m.GuildID {
		return ctx.Reply(fmt.Sprintf("subscription #%d does not exist in this guild.", id))
	}

	err = ctx.bot.c.DestroySubscription(id)
	return ctx.Reply(fmt.Sprintf("subscription #%d has been deleted.", id))
}

// list
func list(ctx *context) error {
	ok, err := checkPrivilege(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}

	gc, err := ctx.bot.c.GetGuildConfig(ctx.m.GuildID)
	if err != nil {
		return err
	}
	subs, err := ctx.bot.c.GetSubscriptions(ctx.m.GuildID)
	if err != nil {
		return err
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("**Guild Contact:** `%s`\n**Embeds?** %v\n**Webhooks?** %v\n\n",
		gc.Contact, gc.Embeds, gc.Webhooks))

	b.WriteString("**Sub ID | Channel | Feed URI | Embed? | Webhook?\n\n**")
	for _, s := range subs {
		b.WriteString(fmt.Sprintf("%d | <#%s> | `%s` | %v | %v\n",
			s.ID, s.ChannelID, s.Feed.URI, fmtBool(s.Overwrite.Embeds), fmtBool(s.Overwrite.Webhooks)))

		if b.Len() > 1900 {
			err = ctx.Reply(b.String())
			if err != nil {
				return err
			}
			b = strings.Builder{}
		}
	}

	return ctx.Reply(b.String())
}

// set <channel|contact|embed|webhook> [...]
func set(ctx *context) error {
	ok, err := checkPrivilege(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}

	if len(ctx.args) == 0 {
		return ctx.Reply("**usage:** set <channel|contact|embed|webhook> ..., see help command.")
	}
	subCommand := ctx.args[0]
	switch subCommand {
	case "channel":
		err = setChannel(ctx)
	case "contact":
		err = setContact(ctx)
	case "embed":
		err = setEmbed(ctx)
	case "webhook":
		err = setWebhook(ctx)
	default:
		err = ctx.Reply("subcommand must be one of channel|contact|embed|webhook, see help command.")
	}
	return err
}

// set channel <id> [channel]
func setChannel(ctx *context) error {
	if len(ctx.args) < 2 {
		return ctx.Reply("**usage:** `set channel <id> [channel]`; please omit spaces from arguments?!")
	}

	var channelID string
	if len(ctx.args) == 3 {
		c := ctx.args[2]
		if !channelRegex.MatchString(c) {
			return ctx.Reply("when specifying a channel ID, please use a #channel mention!")
		}
		// <#...>
		channelID = c[2 : len(c)-1]
	} else {
		channelID = ctx.m.ChannelID
	}

	id, err := strconv.Atoi(ctx.args[1])
	if err != nil {
		return ctx.Reply("`id` must be a number!")
	}
	sub, err := ctx.bot.c.GetSubscription(id)
	if err == sql.ErrNoRows {
		return ctx.Reply("could not find a subscription with that ID, check the list again?")
	} else if err != nil {
		return err
	}

	if sub.GuildID != ctx.m.GuildID {
		return ctx.Reply(fmt.Sprintf("subscription #%d does not exist in this guild.", id))
	}

	err = ctx.bot.c.ModifySubscriptionChannel(id, channelID)
	if err != nil {
		return err
	}

	return ctx.Reply(fmt.Sprintf("subscription #%d will now write to <#%s>", id, channelID))
}

// set contact <user|channel>
func setContact(ctx *context) error {
	if len(ctx.args) != 2 {
		return ctx.Reply("**usage:** `set contact <user|channel>`; please use a user mention, user id, or channel mention, and omit spaces.")
	}
	arg := ctx.args[1]

	var id string
	if channelRegex.MatchString(arg) {
		// <#...>
		c := arg[2 : len(arg)-1]
		id = "c:" + c
	} else if len(ctx.m.Mentions) > 0 {
		u := ctx.m.Mentions[0].ID
		id = "u:" + u
	} else if _, err := strconv.Atoi(arg); err == nil {
		id = "u:" + arg
	} else {
		return ctx.Reply("contact must be a user mention, user id, or channel mention; not a user name or channel name.")
	}

	err := ctx.bot.c.ModifyGuildContact(ctx.m.GuildID, id)
	if err != nil {
		return err
	}
	return ctx.Reply("the guild's contact has been changed.")
}

// set embed <on|off|inherit> [id]
func setEmbed(ctx *context) error {
	if len(ctx.args) < 2 {
		return ctx.Reply("**usage:** `set embed <on|off|inherit> [id]`")
	}

	a := ctx.args[1]
	var val sql.NullBool
	if a == "on" {
		val = sql.NullBool{Bool:true, Valid:true}
	} else if a == "off" {
		val = sql.NullBool{Bool:false, Valid:true}
	} else if a == "inherit" {
		val = sql.NullBool{Valid:false}
	} else {
		return ctx.Reply("parameter must be one of on|off")
	}

	if len(ctx.args) == 2 {
		if !val.Valid {
			return ctx.Reply("`inherit` is only a valid flag on overwrites, please specify on|off")
		}
		err := ctx.bot.c.ModifyGuildEmbeds(ctx.m.GuildID, val.Bool)
		if err != nil {
			return err
		}
	} else {
		id, err := strconv.Atoi(ctx.args[2])
		if err != nil {
			return ctx.Reply("`id` must be a number!")
		}
		sub, err := ctx.bot.c.GetSubscription(id)
		if err == sql.ErrNoRows {
			return ctx.Reply("could not find a subscription with that ID, check the list again?")
		} else if err != nil {
			return err
		}

		if sub.GuildID != ctx.m.GuildID {
			return ctx.Reply(fmt.Sprintf("subscription #%d does not exist in this guild.", id))
		}

		err = ctx.bot.c.ModifyOverwriteEmbeds(sub.ID, val)
		if err != nil {
			return err
		}
	}

	if val.Bool {
		return ctx.Reply("feedbot will now post updates in this guild using embeds, unless overridden elsewhere.")
	}
	if val.Valid {
		return ctx.Reply("feedbot will no longer post updates in this guild using embeds, unless overridden elsewhere.")
	}
	return ctx.Reply("feedbot will default to the guild-wide behavior for embeds.")
}

// set webhook <on|off> [id]
func setWebhook(ctx *context) error {
	if len(ctx.args) < 2 {
		return ctx.Reply("**usage:** `set webhook <on|off> [id]`")
	}

	a := ctx.args[1]
	var val sql.NullBool
	if a == "on" {
		val = sql.NullBool{Bool:true, Valid:true}
	} else if a == "off" {
		val = sql.NullBool{Bool:false, Valid:true}
	} else if a == "inherit" {
		val = sql.NullBool{Valid:false}
	} else {
		return ctx.Reply("parameter must be one of on|off")
	}

	if len(ctx.args) == 2 {
		if !val.Valid {
			return ctx.Reply("`inherit` is only a valid flag on overwrites, please specify on|off")
		}
		err := ctx.bot.c.ModifyGuildWebhooks(ctx.m.GuildID, val.Bool)
		if err != nil {
			return err
		}
	} else {
		id, err := strconv.Atoi(ctx.args[2])
		if err != nil {
			return ctx.Reply("`id` must be a number!")
		}
		sub, err := ctx.bot.c.GetSubscription(id)
		if err == sql.ErrNoRows {
			return ctx.Reply("could not find a subscription with that ID, check the list again?")
		} else if err != nil {
			return err
		}

		if sub.GuildID != ctx.m.GuildID {
			return ctx.Reply(fmt.Sprintf("subscription #%d does not exist in this guild.", id))
		}

		err = ctx.bot.c.ModifyOverwriteWebhooks(sub.ID, val)
		if err != nil {
			return err
		}
	}

	if val.Bool {
		return ctx.Reply("feedbot will now post updates in this guild using webhooks, unless overridden elsewhere.")
	}
	if val.Valid {
		return ctx.Reply("feedbot will no longer post updates in this guild using webhooks, unless overridden elsewhere.")
	}
	return ctx.Reply("feedbot will default to the guild-wide behavior for webhooks.")
}

const adminOnly = "Sorry, feedbot requires the **ADMINISTRATOR** privilege!"

func checkPrivilege(ctx *context) (bool, error) {
	ok, err := memberHasPermission(ctx.s, ctx.m.GuildID, ctx.m.Author.ID, discordgo.PermissionAdministrator)
	if err != nil {
		return false, err
	}
	if !ok {
		if err = ctx.Reply(adminOnly); err != nil {
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

func findChannel(ctx *context, id string) (*discordgo.Channel, error) {
	channel, err := ctx.s.State.Channel(id)
	if err != nil {
		return nil, errors.Wrap(err, "err fetching channel from state")
	}
	if channel == nil {
		channel, err = ctx.s.Channel(id)
		if err != nil {
			return nil, errors.Wrap(err, "err fetching channel from api")
		}
	}
	return channel, nil
}

// dbg~migrate
func dbgMigrate(ctx *context) error {
	if ctx.m.Author.ID != owner {
		return nil
	}

	guild, err := ctx.s.State.Guild(ctx.m.GuildID)
	if err != nil {
		return err
	}
	if guild == nil {
		ctx.Reply("couldn't find the guild not gonna try")
	}

	c := "u:" + guild.OwnerID
	err = ctx.bot.c.CreateGuildConfig(guild.ID, c)
	if err != nil {
		return err
	}
	return ctx.Reply("gotem")
}

func fmtBool(v sql.NullBool) string {
	if !v.Valid {
		return "inherit"
	} else if v.Bool {
		return "on"
	} else {
		return "false"
	}
}