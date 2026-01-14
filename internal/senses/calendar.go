package senses

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/vthunder/bud2/internal/integrations/calendar"
	"github.com/vthunder/bud2/internal/memory"
)

// DefaultCalendarPollInterval is how often to check for upcoming events
const DefaultCalendarPollInterval = 5 * time.Minute

// DefaultMeetingReminderBefore is how far ahead to send meeting reminders
const DefaultMeetingReminderBefore = 15 * time.Minute

// CalendarSense monitors Google Calendar and produces percepts for events
type CalendarSense struct {
	client         *calendar.Client
	inbox          *memory.Inbox
	pollInterval   time.Duration
	reminderBefore time.Duration
	timezone       *time.Location // User's timezone for daily agenda timing

	// State tracking
	mu              sync.RWMutex
	lastPoll        time.Time
	notifiedEvents  map[string]time.Time // eventID -> when we notified
	lastDailyAgenda time.Time            // when we last sent daily agenda

	// Control
	stopChan chan struct{}
	stopped  bool

	// Callbacks
	onError func(err error)
}

// CalendarConfig holds configuration for the calendar sense
type CalendarConfig struct {
	Client         *calendar.Client
	PollInterval   time.Duration
	ReminderBefore time.Duration
	Timezone       *time.Location // User's timezone for daily agenda timing
}

// NewCalendarSense creates a new calendar sense
func NewCalendarSense(cfg CalendarConfig, inbox *memory.Inbox) *CalendarSense {
	if cfg.PollInterval == 0 {
		cfg.PollInterval = DefaultCalendarPollInterval
	}
	if cfg.ReminderBefore == 0 {
		cfg.ReminderBefore = DefaultMeetingReminderBefore
	}
	if cfg.Timezone == nil {
		cfg.Timezone = time.UTC // Default to UTC if not specified
	}

	return &CalendarSense{
		client:         cfg.Client,
		inbox:          inbox,
		pollInterval:   cfg.PollInterval,
		reminderBefore: cfg.ReminderBefore,
		timezone:       cfg.Timezone,
		notifiedEvents: make(map[string]time.Time),
		stopChan:       make(chan struct{}),
	}
}

// Start begins polling the calendar
func (c *CalendarSense) Start() error {
	log.Printf("[calendar-sense] Starting with poll interval %v, reminder before %v",
		c.pollInterval, c.reminderBefore)

	go c.pollLoop()
	return nil
}

// Stop stops the calendar polling
func (c *CalendarSense) Stop() error {
	c.mu.Lock()
	if c.stopped {
		c.mu.Unlock()
		return nil
	}
	c.stopped = true
	close(c.stopChan)
	c.mu.Unlock()

	log.Printf("[calendar-sense] Stopped")
	return nil
}

// SetOnError sets an error callback
func (c *CalendarSense) SetOnError(callback func(err error)) {
	c.mu.Lock()
	c.onError = callback
	c.mu.Unlock()
}

func (c *CalendarSense) pollLoop() {
	// Do an initial poll immediately
	c.poll()

	ticker := time.NewTicker(c.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopChan:
			return
		case <-ticker.C:
			c.poll()
		}
	}
}

func (c *CalendarSense) poll() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	c.mu.Lock()
	c.lastPoll = time.Now()
	c.mu.Unlock()

	// Check for daily agenda (send once per day, in the morning)
	c.checkDailyAgenda(ctx)

	// Check for upcoming meetings that need reminders
	c.checkUpcomingMeetings(ctx)

	// Clean up old notification records (older than 24 hours)
	c.cleanupNotifications()
}

func (c *CalendarSense) checkDailyAgenda(ctx context.Context) {
	// Use user's timezone for daily agenda timing
	nowUTC := time.Now()
	nowLocal := nowUTC.In(c.timezone)
	today := time.Date(nowLocal.Year(), nowLocal.Month(), nowLocal.Day(), 0, 0, 0, 0, c.timezone)

	c.mu.RLock()
	lastAgenda := c.lastDailyAgenda
	c.mu.RUnlock()

	// Send daily agenda between 7 AM and 9 AM in user's timezone, once per day
	hour := nowLocal.Hour()
	if hour < 7 || hour >= 9 {
		return
	}

	// Check if we already sent today
	if !lastAgenda.IsZero() && lastAgenda.After(today) {
		return
	}

	// Get today's events
	events, err := c.client.GetTodayEvents(ctx)
	if err != nil {
		log.Printf("[calendar-sense] Failed to get today's events: %v", err)
		c.handleError(err)
		return
	}

	// Filter to only confirmed events with content
	var relevantEvents []calendar.Event
	for _, e := range events {
		if e.Status != "cancelled" && e.Summary != "" {
			relevantEvents = append(relevantEvents, e)
		}
	}

	if len(relevantEvents) == 0 {
		log.Printf("[calendar-sense] No events today, skipping daily agenda")
		c.mu.Lock()
		c.lastDailyAgenda = nowUTC
		c.mu.Unlock()
		return
	}

	// Create daily agenda message
	agenda := c.formatDailyAgenda(relevantEvents)

	msg := &memory.InboxMessage{
		ID:        fmt.Sprintf("calendar-agenda-%s", today.Format("2006-01-02")),
		Type:      "impulse",
		Subtype:   "daily_agenda",
		Content:   agenda,
		Timestamp: nowUTC,
		Status:    "pending",
		Extra: map[string]any{
			"source":       "calendar",
			"impulse_type": "daily_agenda",
			"event_count":  len(relevantEvents),
			"date":         today.Format("2006-01-02"),
		},
	}

	c.inbox.Add(msg)

	c.mu.Lock()
	c.lastDailyAgenda = nowUTC
	c.mu.Unlock()

	log.Printf("[calendar-sense] Sent daily agenda with %d events", len(relevantEvents))
}

func (c *CalendarSense) checkUpcomingMeetings(ctx context.Context) {
	now := time.Now()

	// Look ahead for events starting within reminder window + poll interval
	// This ensures we don't miss events between polls
	lookAhead := c.reminderBefore + c.pollInterval
	events, err := c.client.GetUpcomingEvents(ctx, lookAhead, 10)
	if err != nil {
		log.Printf("[calendar-sense] Failed to get upcoming events: %v", err)
		c.handleError(err)
		return
	}

	for _, event := range events {
		// Skip cancelled events
		if event.Status == "cancelled" {
			continue
		}

		// Skip all-day events for meeting reminders
		if event.AllDay {
			continue
		}

		// Check if event starts within reminder window
		timeUntil := time.Until(event.Start)
		if timeUntil > c.reminderBefore || timeUntil < 0 {
			continue
		}

		// Check if we already notified for this event
		notifyKey := fmt.Sprintf("%s-%s", event.ID, event.Start.Format("2006-01-02"))
		c.mu.RLock()
		_, alreadyNotified := c.notifiedEvents[notifyKey]
		c.mu.RUnlock()

		if alreadyNotified {
			continue
		}

		// Create meeting reminder
		c.sendMeetingReminder(event, timeUntil)

		c.mu.Lock()
		c.notifiedEvents[notifyKey] = now
		c.mu.Unlock()
	}
}

func (c *CalendarSense) sendMeetingReminder(event calendar.Event, timeUntil time.Duration) {
	// Format reminder content
	content := fmt.Sprintf("Upcoming meeting in %s: %s", formatDuration(timeUntil), event.Summary)
	if event.Location != "" {
		content += fmt.Sprintf("\nLocation: %s", event.Location)
	}
	if event.MeetLink != "" {
		content += fmt.Sprintf("\nVideo call: %s", event.MeetLink)
	}

	// Build attendee info
	var attendeeInfo string
	if len(event.Attendees) > 0 {
		var names []string
		for _, a := range event.Attendees {
			if a.Self {
				continue // Skip self
			}
			name := a.DisplayName
			if name == "" {
				name = a.Email
			}
			names = append(names, name)
		}
		if len(names) > 0 {
			attendeeInfo = fmt.Sprintf("With: %s", joinMax(names, 5))
		}
	}
	if attendeeInfo != "" {
		content += "\n" + attendeeInfo
	}

	// Calculate intensity based on time until event
	intensity := 0.7
	if timeUntil < 5*time.Minute {
		intensity = 0.9 // More urgent if very soon
	} else if timeUntil < 10*time.Minute {
		intensity = 0.8
	}

	msg := &memory.InboxMessage{
		ID:        fmt.Sprintf("calendar-reminder-%s-%d", event.ID, time.Now().UnixNano()),
		Type:      "impulse",
		Subtype:   "meeting_reminder",
		Content:   content,
		Timestamp: time.Now(),
		Status:    "pending",
		Extra: map[string]any{
			"source":       "calendar",
			"event_id":     event.ID,
			"event_title":  event.Summary,
			"event_start":  event.Start.Format(time.RFC3339),
			"event_end":    event.End.Format(time.RFC3339),
			"time_until":   timeUntil.String(),
			"has_meet":     event.MeetLink != "",
			"attendees":    len(event.Attendees),
			"intensity":    intensity,
			"meet_link":    event.MeetLink,
			"location":     event.Location,
			"description":  event.Description,
		},
	}

	c.inbox.Add(msg)
	log.Printf("[calendar-sense] Sent meeting reminder: %s (in %s)", event.Summary, formatDuration(timeUntil))
}

func (c *CalendarSense) formatDailyAgenda(events []calendar.Event) string {
	agenda := fmt.Sprintf("Daily agenda for %s:\n\n", time.Now().Format("Monday, January 2"))

	for i, event := range events {
		agenda += fmt.Sprintf("%d. %s\n", i+1, event.FormatEventSummary())

		if len(event.Attendees) > 0 {
			var names []string
			for _, a := range event.Attendees {
				if a.Self {
					continue
				}
				name := a.DisplayName
				if name == "" {
					name = a.Email
				}
				names = append(names, name)
			}
			if len(names) > 0 {
				agenda += fmt.Sprintf("   With: %s\n", joinMax(names, 3))
			}
		}
	}

	return agenda
}

func (c *CalendarSense) cleanupNotifications() {
	c.mu.Lock()
	defer c.mu.Unlock()

	cutoff := time.Now().Add(-24 * time.Hour)
	for key, notifiedAt := range c.notifiedEvents {
		if notifiedAt.Before(cutoff) {
			delete(c.notifiedEvents, key)
		}
	}
}

func (c *CalendarSense) handleError(err error) {
	c.mu.RLock()
	callback := c.onError
	c.mu.RUnlock()

	if callback != nil {
		callback(err)
	}
}

// LastPoll returns when the calendar was last polled
func (c *CalendarSense) LastPoll() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastPoll
}

// formatDuration formats a duration in a human-readable way
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return "less than a minute"
	}
	if d < 2*time.Minute {
		return "1 minute"
	}
	if d < time.Hour {
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	}
	if d < 2*time.Hour {
		return "1 hour"
	}
	return fmt.Sprintf("%d hours", int(d.Hours()))
}

// joinMax joins strings with a max count, adding "and X more" if needed
func joinMax(items []string, max int) string {
	if len(items) <= max {
		return joinStrings(items)
	}
	return joinStrings(items[:max]) + fmt.Sprintf(" and %d more", len(items)-max)
}

func joinStrings(items []string) string {
	if len(items) == 0 {
		return ""
	}
	if len(items) == 1 {
		return items[0]
	}
	if len(items) == 2 {
		return items[0] + " and " + items[1]
	}
	result := ""
	for i, item := range items {
		if i == len(items)-1 {
			result += "and " + item
		} else {
			result += item + ", "
		}
	}
	return result
}
