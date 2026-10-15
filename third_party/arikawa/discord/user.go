package discord

import (
	"strconv"
	"time"

	"github.com/ayn2op/arikawa/v3/utils/json/option"
)

type User struct {
	ID            UserID `json:"id"`
	Username      string `json:"username"`
	Discriminator string `json:"discriminator"` // This is "0" if the user has migrated to the new username system.
	Avatar        Hash   `json:"avatar"`
	DisplayName   string `json:"global_name"`

	// These fields may be omitted

	Bot bool `json:"bot,omitempty"`
	MFA bool `json:"mfa_enabled,omitempty"`

	Nitro       UserNitro `json:"premium_type,omitempty"`
	Flags       UserFlags `json:"flags,omitempty"`
	PublicFlags UserFlags `json:"public_flags,omitempty"`

	DiscordSystem bool `json:"system,omitempty"`
	EmailVerified bool `json:"verified,omitempty"`

	Locale string `json:"locale,omitempty"`
	Email  string `json:"email,omitempty"`

	Banner Hash  `json:"banner,omitempty"`
	Accent Color `json:"accent_color,omitempty"`

	// NSFWAllowed indicates whether the user is allowed to see NSFW content, `nil` if not yet known.
	NSFWAllowed option.Option[bool] `json:"nsfw_allowed,omitempty"`
	Pronouns    string              `json:"pronouns,omitempty"`
	Bio         string              `json:"bio,omitempty"`

	// AvatarDecoration is the frame drawn around the user's avatar, if any.
	AvatarDecoration *AvatarDecoration `json:"avatar_decoration_data,omitempty"`
	// Collectibles are what the user decorates their name with, such as the
	// nameplate behind it in member lists.
	Collectibles *Collectibles `json:"collectibles,omitempty"`
	// PrimaryGuild is the tag of a server that the user wears by their name.
	PrimaryGuild *PrimaryGuild `json:"primary_guild,omitempty"`
	// DisplayNameStyles are the font, effect and colors of the user's name,
	// as Nitro lets them choose, if any.
	DisplayNameStyles *DisplayNameStyles `json:"display_name_styles,omitempty"`
}

// AvatarDecoration is a frame drawn around an avatar.
type AvatarDecoration struct {
	// Asset names the picture of the frame.
	Asset string    `json:"asset"`
	SKUID Snowflake `json:"sku_id,omitempty"`
}

// URL returns the picture of the frame: an APNG if animated is true and the
// frame moves, and a still PNG otherwise.
func (d AvatarDecoration) URL(animated bool) string {
	return "https://cdn.discordapp.com/avatar-decoration-presets/" + d.Asset + ".png?size=96&passthrough=" + strconv.FormatBool(animated)
}

// Collectibles are what a user decorates their name with.
type Collectibles struct {
	Nameplate *Nameplate `json:"nameplate,omitempty"`
}

// Nameplate is the picture behind a user's name in member lists.
type Nameplate struct {
	// Asset is the path of the nameplate's pictures, ending with a slash.
	Asset string `json:"asset"`
	// Palette names the colors that go with the picture.
	Palette string    `json:"palette,omitempty"`
	Label   string    `json:"label,omitempty"`
	SKUID   Snowflake `json:"sku_id,omitempty"`
}

// URL returns the still picture of the nameplate.
func (n Nameplate) URL() string {
	return "https://cdn.discordapp.com/assets/collectibles/" + n.Asset + "static.png"
}

// VideoURL returns the nameplate moving, a short looping video, which
// Discord plays under the pointer.
func (n Nameplate) VideoURL() string {
	return "https://cdn.discordapp.com/assets/collectibles/" + n.Asset + "asset.webm"
}

// PrimaryGuild is the tag of a server that a user wears by their name.
type PrimaryGuild struct {
	GuildID GuildID `json:"identity_guild_id,omitempty"`
	Enabled bool    `json:"identity_enabled,omitempty"`
	Tag     string  `json:"tag,omitempty"`
	// Badge is the hash of the small picture before the tag.
	Badge string `json:"badge,omitempty"`
}

// CreatedAt returns a time object representing when the user was created.
func (u User) CreatedAt() time.Time {
	return u.ID.Time()
}

// Mention returns a mention of the user.
func (u User) Mention() string {
	return u.ID.Mention()
}

// Tag returns a tag of the user.
func (u User) Tag() string {
	if u.Discriminator == "0" || u.Discriminator == "0000" {
		return u.Username
	}
	return u.Username + "#" + u.Discriminator
}

// DisplayOrUsername returns the DisplayName if it is set, otherwise the
// Username.
func (u User) DisplayOrUsername() string {
	if u.DisplayName != "" {
		return u.DisplayName
	}
	return u.Username
}

// DisplayOrTag returns the DisplayName if it is set, otherwise the Tag.
func (u User) DisplayOrTag() string {
	if u.DisplayName != "" {
		return u.DisplayName
	}
	return u.Tag()
}

// AvatarURL returns the URL of the Avatar Image. It automatically detects a
// suitable type.
func (u User) AvatarURL() string {
	return u.AvatarURLWithType(AutoImage)
}

// AvatarURLWithType returns the URL of the Avatar Image using the passed type.
// If the user has no Avatar, his default avatar will be returned. This
// requires ImageType Auto or PNG
//
// Supported Image Types: PNG, JPEG, WebP, GIF (read above for caveat)
func (u User) AvatarURLWithType(t ImageType) string {
	if u.Avatar == "" {
		if t != PNGImage && t != AutoImage {
			return ""
		}

		var picNo string
		if u.Discriminator != "0" {
			disc, err := strconv.Atoi(u.Discriminator)
			if err != nil { // this should never happen
				return ""
			}
			picNo = strconv.Itoa(disc % 5)
		} else {
			picNo = strconv.FormatUint(uint64(u.ID>>22)%6, 10)
		}

		return "https://cdn.discordapp.com/embed/avatars/" + picNo + ".png"
	}

	return "https://cdn.discordapp.com/avatars/" + u.ID.String() + "/" + t.format(u.Avatar)
}

// BannerURL returns the URL of the Banner Image. It automatically detects a
// suitable type.
func (u User) BannerURL() string {
	return u.BannerURLWithType(AutoImage)
}

// BannerURLWithType returns the URL of the Banner Image using the passed type.
// If the user has no Banner, an empty string will be returned.
func (u User) BannerURLWithType(t ImageType) string {
	if u.Banner == "" {
		return ""
	}

	return "https://cdn.discordapp.com/banners/" + u.ID.String() + "/" + t.format(u.Banner)
}

type UserFlags uint32

const NoFlag UserFlags = 0

const (
	Employee UserFlags = 1 << iota
	Partner
	HypeSquadEvents
	BugHunterLvl1
	_
	_
	HouseBravery
	HouseBrilliance
	HouseBalance
	EarlySupporter
	TeamUser
	_
	System
	_
	BugHunterLvl2
	_
	VerifiedBot
	VerifiedBotDeveloper
	CertifiedModerator
	LikelySpammer // undocumented
)

type UserNitro uint8

const (
	NoUserNitro UserNitro = iota
	NitroClassic
	NitroFull
	NitroBasic
)

type Connection struct {
	ID   string  `json:"id"`
	Name string  `json:"name"`
	Type Service `json:"type"`

	Revoked      bool `json:"revoked"`
	Verified     bool `json:"verified"`
	FriendSync   bool `json:"friend_sync"`
	ShowActivity bool `json:"show_activity"`

	Visibility ConnectionVisibility `json:"visibility"`

	// Only partial
	Integrations []Integration `json:"integrations"`
}

type ConnectionVisibility uint8

const (
	ConnectionNotVisible ConnectionVisibility = iota
	ConnectionVisibleEveryone
)

type Activity struct {
	Name string `json:"name"`
	URL  URL    `json:"url,omitempty"`

	Type ActivityType `json:"type"`

	// User only

	Instance bool          `json:"instance,omitempty"`
	Flags    ActivityFlags `json:"flags,omitempty"`

	CreatedAt  UnixMsTimestamp     `json:"created_at,omitempty"`
	Timestamps *ActivityTimestamps `json:"timestamps,omitempty"`

	AppID   AppID  `json:"application_id,omitempty"`
	Details string `json:"details,omitempty"`
	State   string `json:"state,omitempty"` // party status
	Emoji   *Emoji `json:"emoji,omitempty"`

	Party   *ActivityParty   `json:"party,omitempty"`
	Assets  *ActivityAssets  `json:"assets,omitempty"`
	Secrets *ActivitySecrets `json:"secrets,omitempty"`

	// Undocumented fields
	SyncID    string `json:"sync_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	// Buttons are the labels of the activity's buttons, whose links are in
	// Metadata.
	Buttons  []string          `json:"buttons,omitempty"`
	Metadata *ActivityMetadata `json:"metadata,omitempty"`
}

// ActivityMetadata is more about an activity, such as where its buttons
// lead.
type ActivityMetadata struct {
	ButtonURLs []string `json:"button_urls,omitempty"`
}

type ActivityType uint8

const (
	GameActivity      ActivityType = iota // Playing $name
	StreamingActivity                     // Streaming $details
	ListeningActivity                     // Listening to $name
	WatchingActivity                      // Watching $name
	CustomActivity                        // $emoji $state
	CompetingActivity                     // Competing in $name
)

type ActivityFlags uint32

const (
	InstanceActivity ActivityFlags = 1 << iota
	JoinActivity
	SpectateActivity
	JoinRequestActivity
	SyncActivity
	PlayActivity
)

type ActivityTimestamps struct {
	Start UnixMsTimestamp `json:"start,omitempty"`
	End   UnixMsTimestamp `json:"end,omitempty"`
}

type ActivityParty struct {
	ID   string `json:"id,omitempty"`
	Size [2]int `json:"size,omitempty"` // [ current, max ]
}

type ActivityAssets struct {
	LargeImage string `json:"large_image,omitempty"` // id
	LargeText  string `json:"large_text,omitempty"`
	SmallImage string `json:"small_image,omitempty"` // id
	SmallText  string `json:"small_text,omitempty"`
}

type ActivitySecrets struct {
	Join     string `json:"join,omitempty"`
	Spectate string `json:"spectate,omitempty"`
	Match    string `json:"match,omitempty"`
}

// A Relationship between the logged in user and the user in the struct. This
// struct is undocumented.
type Relationship struct {
	UserID   UserID                `json:"id"`
	User     User                  `json:"user"`
	Type     RelationshipType      `json:"type"`
	Since    Timestamp             `json:"since,omitzero"`
	Nickname option.Option[string] `json:"nickname"`
}

// RelationshipType is an enum for a relationship.
type RelationshipType uint8

const (
	_ RelationshipType = iota
	FriendRelationship
	BlockedRelationship
	IncomingFriendRequest
	SentFriendRequest
)

// DisplayNameStyles is how a user's name is drawn: in the font FontID, with
// the effect EffectID, in Colors, RGB.
type DisplayNameStyles struct {
	FontID   int      `json:"font_id"`
	EffectID int      `json:"effect_id"`
	Colors   []uint32 `json:"colors"`
}

// The effects of a styled name.
const (
	DisplayNameEffectSolid    = 1
	DisplayNameEffectGradient = 2
)
