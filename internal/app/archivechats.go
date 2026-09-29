package app

// archiveChats puts chats away, or brings them back. Nothing is deleted, and
// an open chat stays open: archiving only changes which part of the list it is
// drawn in.
func (a *App) archiveChats(ids []int64, archived bool) {
	if a.store == nil {
		return
	}
	for _, id := range ids {
		if err := a.store.SetChatArchived(id, archived); err != nil {
			verb := "archive"
			if !archived {
				verb = "unarchive"
			}
			a.toast("Could not " + verb + ": " + err.Error())
			break
		}
	}
	a.refreshSidebar()
}
