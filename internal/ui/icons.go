package ui

// Icon names for the set Astral bundles. Constants rather than string
// literals at the call site, so a typo is a compile error instead of a button
// that silently renders blank.
//
// The files are embedded and unpacked by internal/app; see icons.go there.
const (
	IconSend       = "astral-send-symbolic"
	IconStop       = "astral-stop-symbolic"
	IconCopy       = "astral-copy-symbolic"
	IconRegenerate = "astral-regenerate-symbolic"
	IconTrash      = "astral-trash-symbolic"
	IconEdit       = "astral-edit-symbolic"
	IconMenu       = "astral-menu-symbolic"
	IconPanelLeft  = "astral-panel-left-symbolic"
	IconChat       = "astral-chat-symbolic"
	IconCharacters = "astral-characters-symbolic"
	IconDesigner   = "astral-designer-symbolic"
	IconSettings   = "astral-settings-symbolic"
	IconFolder     = "astral-folder-symbolic"
	IconAdd        = "astral-add-symbolic"
	IconSearch     = "astral-search-symbolic"
	IconHistory    = "astral-history-symbolic"
	IconInfo       = "astral-info-symbolic"
)

// AllIcons is every name the app uses, so the dev harness can check that none
// of them silently fail to resolve.
var AllIcons = []string{
	IconSend, IconStop, IconCopy, IconRegenerate, IconTrash, IconEdit,
	IconMenu, IconPanelLeft, IconChat, IconCharacters, IconDesigner,
	IconSettings, IconFolder, IconAdd, IconSearch, IconHistory, IconInfo,
}
