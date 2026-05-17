package store

import "time"

// monthlyIntervalDays marks a calendar-monthly schedule (see day_of_month).
const monthlyIntervalDays = 0

func isMonthlyAllowance(_ int, dayOfMonth *int) bool {
	return dayOfMonth != nil && *dayOfMonth >= 1
}

func isWeekdayAllowance(intervalDays int) bool {
	return intervalDays == 7 || intervalDays == 14
}

func clampDayInMonth(year int, month time.Month, day int) time.Time {
	if day < 1 {
		day = 1
	}
	last := lastDayOfMonth(year, month)
	if day > last {
		day = last
	}
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func lastDayOfMonth(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// nextMonthlyDue is the next calendar monthly due instant strictly after now is not required;
// if this month's day is still in the future, use it; otherwise next month.
func nextMonthlyDue(now time.Time, dayOfMonth int) time.Time {
	now = now.UTC()
	candidate := clampDayInMonth(now.Year(), now.Month(), dayOfMonth)
	if candidate.After(now) {
		return candidate
	}
	return advanceMonthlyDue(candidate, dayOfMonth)
}

// advanceMonthlyDue returns the next calendar due date after the one just paid.
func advanceMonthlyDue(paidDue time.Time, dayOfMonth int) time.Time {
	paidDue = paidDue.UTC()
	y, m, _ := paidDue.Date()
	m++
	if m > 12 {
		m = 1
		y++
	}
	return clampDayInMonth(y, m, dayOfMonth)
}

// nextWeekdayDue is the next midnight UTC on the given weekday strictly after now.
func nextWeekdayDue(now time.Time, weekday time.Weekday) time.Time {
	now = now.UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	offset := (int(weekday) - int(today.Weekday()) + 7) % 7
	candidate := today.AddDate(0, 0, offset)
	if !candidate.After(now) {
		candidate = candidate.AddDate(0, 0, 7)
	}
	return candidate
}

func initialNextDue(now time.Time, intervalDays int, dayOfMonth, dayOfWeek *int) time.Time {
	if isMonthlyAllowance(intervalDays, dayOfMonth) {
		return nextMonthlyDue(now, *dayOfMonth)
	}
	if isWeekdayAllowance(intervalDays) && dayOfWeek != nil {
		return nextWeekdayDue(now, time.Weekday(*dayOfWeek))
	}
	return now.UTC()
}

func advanceAllowanceDue(paidDue time.Time, intervalDays int, dayOfMonth, dayOfWeek *int) time.Time {
	if isMonthlyAllowance(intervalDays, dayOfMonth) {
		day := 1
		if dayOfMonth != nil {
			day = *dayOfMonth
		}
		return advanceMonthlyDue(paidDue, day)
	}
	if intervalDays == 14 {
		return paidDue.UTC().AddDate(0, 0, 14)
	}
	if intervalDays == 7 {
		return paidDue.UTC().AddDate(0, 0, 7)
	}
	return paidDue.UTC().Add(time.Duration(intervalDays) * 24 * time.Hour)
}

func normalizeAllowanceSchedule(intervalDays int, dayOfMonth, dayOfWeek *int) (intervalDaysOut int, dayOfMonthOut, dayOfWeekOut *int, err error) {
	// Legacy clients sent 30 for "monthly".
	if intervalDays == 30 && (dayOfMonth == nil || *dayOfMonth < 1) {
		d := 1
		return monthlyIntervalDays, &d, nil, nil
	}
	if dayOfMonth != nil && *dayOfMonth >= 1 {
		if *dayOfMonth > 31 {
			return 0, nil, nil, ErrAllowanceInvalidDayOfMonth
		}
		return monthlyIntervalDays, dayOfMonth, nil, nil
	}
	if intervalDays == monthlyIntervalDays {
		return 0, nil, nil, ErrAllowanceInvalidDayOfMonth
	}
	if isWeekdayAllowance(intervalDays) {
		if dayOfWeek == nil || *dayOfWeek < 0 || *dayOfWeek > 6 {
			return 0, nil, nil, ErrAllowanceInvalidDayOfWeek
		}
		return intervalDays, nil, dayOfWeek, nil
	}
	if intervalDays < 1 || intervalDays > 366 {
		return 0, nil, nil, ErrAllowanceInvalidInterval
	}
	return intervalDays, nil, nil, nil
}
