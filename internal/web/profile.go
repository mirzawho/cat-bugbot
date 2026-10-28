package web

import (
	"context"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// profile is the live Telegram profile of a reporter, fetched on demand.
type profile struct {
	FirstName string
	LastName  string
	Username  string
	Photos    []string // file_ids, best (largest) first
}

type profileEntry struct {
	p       profile
	fetched time.Time
}

const profileTTL = 10 * time.Minute

type profileCache struct {
	mu      sync.Mutex
	entries map[int64]profileEntry
}

// getProfile returns the reporter's Telegram profile, cached for a few minutes.
// Lookups are best effort: the names stored with the report are used if this fails.
func (s *Server) getProfile(ctx context.Context, userID int64) (profile, bool) {
	s.profiles.mu.Lock()
	entry, ok := s.profiles.entries[userID]
	s.profiles.mu.Unlock()
	if ok && time.Since(entry.fetched) < profileTTL {
		return entry.p, entry.p.FirstName != "" || entry.p.Username != ""
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if s.opts.Lookups == nil {
		return profile{}, false
	}
	chat, err := s.opts.Lookups.GetChat(ctx, userID)
	if err != nil {
		log.Printf("web: getChat %d: %v", userID, err)
		return profile{}, false
	}
	p := profile{FirstName: chat.FirstName, LastName: chat.LastName, Username: chat.Username}
	if photos, err := s.opts.Lookups.GetUserProfilePhotos(ctx, userID); err == nil {
		p.Photos = photos
	}
	s.profiles.mu.Lock()
	s.profiles.entries[userID] = profileEntry{p: p, fetched: time.Now()}
	s.profiles.mu.Unlock()
	return p, true
}

// profilePhoto streams the reporter's profile photo (index 0 is the largest).
// The bot token stays on the server, as with report media.
func (s *Server) profilePhoto(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	index, _ := strconv.Atoi(r.PathValue("index"))
	p, ok := s.getProfile(r.Context(), userID)
	if !ok || index < 0 || index >= len(p.Photos) {
		http.NotFound(w, r)
		return
	}
	f, err := s.opts.Lookups.GetFile(r.Context(), p.Photos[index])
	if err != nil {
		log.Printf("web: profile photo getFile %d: %v", userID, err)
		http.Error(w, "photo unavailable", http.StatusBadGateway)
		return
	}
	raw, err := s.opts.Lookups.DownloadFile(r.Context(), f.FilePath)
	if err != nil {
		log.Printf("web: profile photo download: %v", err)
		http.Error(w, "photo unavailable", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=600")
	_, _ = w.Write(raw)
}

func displayedName(first, last, username string, p profile, ok bool) string {
	switch {
	case ok && p.FirstName != "" && p.LastName != "":
		return p.FirstName + " " + p.LastName
	case ok && p.FirstName != "":
		return p.FirstName
	case first != "" && last != "":
		return first + " " + last
	case first != "":
		return first
	case ok && p.Username != "":
		return p.Username
	case username != "":
		return username
	}
	return "کاربر کت"
}
