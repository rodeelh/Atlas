package automations

import (
	"context"
	"strconv"
	"strings"
	"time"
)

func (m *Module) schedulerLoop(ctx context.Context) {
	interval := m.schedulerInterval
	if interval <= 0 {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.runSchedulerTick(ctx)
		}
	}
}

func (m *Module) runSchedulerTick(ctx context.Context) {
	now := m.schedulerNow().Truncate(time.Minute)
	items, err := m.listDefinitions()
	if err != nil {
		return
	}
	for _, item := range items {
		if !item.IsEnabled {
			continue
		}
		nextRun, ok := nextRunForAutomation(item.ScheduleRaw, item.NextRunAt, now)
		if !ok {
			continue
		}
		slot, due := scheduledSlotAt(nextRun, now)
		if !due {
			continue
		}
		key := item.ID + ":" + slot
		if !m.markSchedulerRun(key, slot) {
			continue
		}
		if meta, following, ok := scheduleState(item.ScheduleRaw, now.Add(time.Minute)); ok {
			item.ScheduleJSON = strPtr(mustJSON(meta, "{}"))
			nextValue := following.UTC().Format(time.RFC3339)
			item.NextRunAt = &nextValue
			item.LastModifiedAt = strPtr(time.Now().UTC().Format(time.RFC3339))
			if _, err := m.saveDefinition(item); err != nil {
				continue
			}
		}
		go func(id string) {
			runCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
			defer cancel()
			_, _ = m.runAutomationSync(runCtx, id, "schedule")
		}(item.ID)
	}
}

func (m *Module) schedulerNow() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

func (m *Module) markSchedulerRun(key, slot string) bool {
	m.schedulerMu.Lock()
	defer m.schedulerMu.Unlock()
	if m.schedulerRuns == nil {
		m.schedulerRuns = map[string]string{}
	}
	if m.schedulerRuns[key] == slot {
		return false
	}
	m.schedulerRuns[key] = slot
	return true
}

func scheduledSlotAt(nextRun, now time.Time) (string, bool) {
	next := nextRun.Truncate(time.Minute)
	current := now.Truncate(time.Minute)
	if current.Before(next) {
		return "", false
	}
	return next.UTC().Format(time.RFC3339), true
}

func nextRunForAutomation(scheduleRaw string, nextRunAt *string, now time.Time) (time.Time, bool) {
	if nextRunAt != nil && strings.TrimSpace(*nextRunAt) != "" {
		if parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(*nextRunAt)); err == nil {
			return parsed, true
		}
	}
	_, next, ok := scheduleState(scheduleRaw, now)
	return next, ok
}

func scheduleState(schedule string, from time.Time) (scheduleMetadata, time.Time, bool) {
	spec, ok := parseSchedule(schedule)
	if !ok {
		return scheduleMetadata{}, time.Time{}, false
	}
	next := nextRunAfter(spec, from)
	return scheduleMetadataFromSpec(spec), next, true
}

func scheduledSlot(schedule string, now time.Time) (string, bool) {
	_, next, ok := scheduleState(schedule, now)
	if !ok {
		return "", false
	}
	return scheduledSlotAt(next, now)
}

func nextRunAfter(spec scheduleSpec, from time.Time) time.Time {
	start := from.Truncate(time.Minute)
	switch spec.kind {
	case "hourly":
		if spec.intervalHours <= 0 {
			spec.intervalHours = 1
		}
		candidate := time.Date(start.Year(), start.Month(), start.Day(), start.Hour(), spec.minute, 0, 0, start.Location())
		for candidate.Before(start) || candidate.Hour()%spec.intervalHours != 0 {
			candidate = candidate.Add(time.Hour)
		}
		return candidate
	case "daily":
		candidate := time.Date(start.Year(), start.Month(), start.Day(), spec.hour, spec.minute, 0, 0, start.Location())
		if candidate.Before(start) {
			candidate = candidate.Add(24 * time.Hour)
		}
		return candidate
	case "weekly":
		for i := 0; i < 8; i++ {
			day := start.AddDate(0, 0, i)
			candidate := time.Date(day.Year(), day.Month(), day.Day(), spec.hour, spec.minute, 0, 0, start.Location())
			if spec.weekdays[candidate.Weekday()] && !candidate.Before(start) {
				return candidate
			}
		}
	}
	return time.Time{}
}

type scheduleSpec struct {
	kind          string
	hour          int
	minute        int
	intervalHours int
	weekdays      map[time.Weekday]bool
}

type scheduleMetadata struct {
	Kind          string   `json:"kind"`
	Hour          int      `json:"hour,omitempty"`
	Minute        int      `json:"minute"`
	IntervalHours int      `json:"intervalHours,omitempty"`
	Weekdays      []string `json:"weekdays,omitempty"`
}

func scheduleMetadataFromSpec(spec scheduleSpec) scheduleMetadata {
	meta := scheduleMetadata{
		Kind:          spec.kind,
		Hour:          spec.hour,
		Minute:        spec.minute,
		IntervalHours: spec.intervalHours,
	}
	if len(spec.weekdays) > 0 {
		order := []time.Weekday{time.Sunday, time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday}
		for _, day := range order {
			if spec.weekdays[day] {
				meta.Weekdays = append(meta.Weekdays, strings.ToLower(day.String()))
			}
		}
	}
	return meta
}

func parseSchedule(schedule string) (scheduleSpec, bool) {
	lower := strings.ToLower(strings.TrimSpace(schedule))
	if lower == "" || strings.HasPrefix(lower, "cron ") || strings.HasPrefix(lower, "once ") {
		return scheduleSpec{}, false
	}
	hour, minute, hasTime := parseScheduleTime(lower)
	if !hasTime {
		hour, minute = 9, 0
	}

	if strings.Contains(lower, "hour") {
		return scheduleSpec{
			kind:          "hourly",
			minute:        minute,
			intervalHours: parseHourlyInterval(lower),
		}, true
	}

	weekdays := parseWeekdays(lower)
	if len(weekdays) > 0 {
		return scheduleSpec{
			kind:     "weekly",
			hour:     hour,
			minute:   minute,
			weekdays: weekdays,
		}, true
	}

	if strings.Contains(lower, "daily") || strings.Contains(lower, "every day") || hasTime {
		return scheduleSpec{
			kind:   "daily",
			hour:   hour,
			minute: minute,
		}, true
	}
	return scheduleSpec{}, false
}

func parseScheduleTime(schedule string) (int, int, bool) {
	cleaned := strings.NewReplacer(",", " ", "@", " ").Replace(schedule)
	formats := []string{"15:04", "3pm", "3am", "3:04pm", "3:04am"}
	for _, part := range strings.Fields(cleaned) {
		part = strings.TrimSpace(part)
		for _, format := range formats {
			if t, err := time.ParseInLocation(format, part, time.Local); err == nil {
				return t.Hour(), t.Minute(), true
			}
		}
	}
	return 0, 0, false
}

func parseHourlyInterval(schedule string) int {
	fields := strings.Fields(schedule)
	for i, field := range fields {
		if field != "every" || i+1 >= len(fields) {
			continue
		}
		if value, err := strconv.Atoi(fields[i+1]); err == nil && value > 0 {
			return value
		}
	}
	return 1
}

func parseWeekdays(schedule string) map[time.Weekday]bool {
	days := map[string]time.Weekday{
		"sunday":    time.Sunday,
		"monday":    time.Monday,
		"tuesday":   time.Tuesday,
		"wednesday": time.Wednesday,
		"thursday":  time.Thursday,
		"friday":    time.Friday,
		"saturday":  time.Saturday,
	}
	out := map[time.Weekday]bool{}
	for name, day := range days {
		if strings.Contains(schedule, name) {
			out[day] = true
		}
	}
	if strings.Contains(schedule, "weekday") {
		out[time.Monday] = true
		out[time.Tuesday] = true
		out[time.Wednesday] = true
		out[time.Thursday] = true
		out[time.Friday] = true
	}
	return out
}
