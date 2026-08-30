/**
 * Ali MDM v1.3 - Screen Sleep Scheduler Types
 * Allows defining automatic screen-off/on times to save energy
 */

export interface ScreenScheduleRule {
  id: string;
  name: string;                  // e.g., "Night (weekdays)", "Weekend"
  enabled: boolean;
  days: number[];                // [0=Sun, 1=Mon, ..., 6=Sat]
  sleepTime: string;             // "HH:MM" — screen off time
  wakeTime: string;              // "HH:MM" — screen on time
}

export interface ScreenSchedulerConfig {
  enabled: boolean;
  rules: ScreenScheduleRule[];
  wakeOnTouch: boolean;          // Allow waking screen by touching during sleep window
}

// Day names for UI
export const DAY_NAMES_SHORT = ['S', 'M', 'T', 'W', 'T', 'F', 'S'];
export const DAY_NAMES_FULL = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];

/**
 * Generate unique rule ID
 */
export const generateRuleId = (): string => {
  return `rule_${Date.now()}_${Math.random().toString(36).substr(2, 9)}`;
};

/**
 * Validate time format HH:MM
 */
export const isValidTime = (time: string): boolean => {
  const regex = /^([01]?[0-9]|2[0-3]):([0-5][0-9])$/;
  return regex.test(time);
};

/**
 * Get current time as "HH:MM" string
 */
export const getCurrentTimeString = (now?: Date): string => {
  const d = now || new Date();
  return `${d.getHours().toString().padStart(2, '0')}:${d.getMinutes().toString().padStart(2, '0')}`;
};

/**
 * Check if the current time falls within a sleep window for a given rule.
 * Handles the midnight-crossing case (e.g., sleepTime=22:00, wakeTime=07:00).
 * 
 * Returns true if the screen should be OFF (sleeping).
 */
export const isRuleActiveNow = (rule: ScreenScheduleRule, now?: Date): boolean => {
  if (!rule.enabled) return false;

  const d = now || new Date();
  const currentDay = d.getDay(); // 0=Sun, 6=Sat

  const currentTime = getCurrentTimeString(d);
  const { sleepTime, wakeTime, days } = rule;

  if (!isValidTime(sleepTime) || !isValidTime(wakeTime)) return false;
  if (sleepTime === wakeTime) return false; // Same time = no window

  if (sleepTime < wakeTime) {
    // Same-day window (e.g., 01:00 → 06:00)
    // Sleep window is within a single day
    if (!days.includes(currentDay)) return false;
    return currentTime >= sleepTime && currentTime < wakeTime;
  } else {
    // Midnight-crossing window (e.g., 22:00 → 07:00)
    // Two cases:
    //   1. After sleepTime on a scheduled day (22:00–23:59)
    //   2. Before wakeTime on the day AFTER a scheduled day (00:00–06:59)
    
    // Case 1: We are in the evening portion (after sleepTime)
    if (currentTime >= sleepTime) {
      // Check if today is a scheduled day
      return days.includes(currentDay);
    }
    
    // Case 2: We are in the morning portion (before wakeTime)
    if (currentTime < wakeTime) {
      // Check if YESTERDAY was a scheduled day
      const yesterday = (currentDay + 6) % 7; // (currentDay - 1 + 7) % 7
      return days.includes(yesterday);
    }
    
    return false;
  }
};

/**
 * Check if the screen should be sleeping based on all enabled rules.
 * Returns the first matching active rule, or null if screen should be ON.
 */
export const getActiveSleepRule = (rules: ScreenScheduleRule[], now?: Date): ScreenScheduleRule | null => {
  for (const rule of rules) {
    if (isRuleActiveNow(rule, now)) {
      return rule;
    }
  }
  return null;
};

/**
 * Check if screen should be sleeping (convenience function)
 */
export const isInSleepWindow = (rules: ScreenScheduleRule[], now?: Date): boolean => {
  return getActiveSleepRule(rules, now) !== null;
};

/**
 * Get days display string for a rule
 */
export const getDaysDisplayString = (days: number[]): string => {
  if (days.length === 0) return 'No days';
  if (days.length === 7) return 'Every day';
  if (days.length === 5 && !days.includes(0) && !days.includes(6)) return 'Weekdays';
  if (days.length === 2 && days.includes(0) && days.includes(6)) return 'Weekends';

  return days
    .sort((a, b) => a - b)
    .map(d => DAY_NAMES_FULL[d].substring(0, 3))
    .join(', ');
};

/**
 * Create a default new rule
 */
export const createDefaultRule = (name?: string): ScreenScheduleRule => ({
  id: generateRuleId(),
  name: name || 'Night',
  enabled: true,
  days: [1, 2, 3, 4, 5], // Weekdays by default
  sleepTime: '22:00',
  wakeTime: '07:00',
});

/**
 * Parse "HH:MM" into { hours, minutes }
 */
const parseTime = (time: string): { hours: number; minutes: number } => {
  const [h, m] = time.split(':').map(Number);
  return { hours: h, minutes: m };
};

/**
 * Compute the next occurrence of the wake time based on the active sleep rule.
 * When we enter sleep, we need to know WHEN to wake up.
 * Returns a Date object for the next wake-up moment.
 */
export const getNextWakeTime = (rule: ScreenScheduleRule, now?: Date): Date | null => {
  if (!rule.enabled || !isValidTime(rule.wakeTime)) return null;
  
  const d = now || new Date();
  const { hours, minutes } = parseTime(rule.wakeTime);
  const currentTime = getCurrentTimeString(d);

  if (rule.sleepTime < rule.wakeTime) {
    // Same-day window (e.g., 01:00 → 06:00) — wake is today
    const wakeDate = new Date(d);
    wakeDate.setHours(hours, minutes, 0, 0);
    if (wakeDate > d) return wakeDate;
    // Already past wake time today — next scheduled day
    return getNextTimeOnScheduledDay(rule.wakeTime, rule.days, d, 1);
  } else {
    // Midnight-crossing (e.g., 22:00 → 07:00)
    if (currentTime >= rule.sleepTime) {
      // We're in the evening portion — wake is tomorrow morning
      const wakeDate = new Date(d);
      wakeDate.setDate(wakeDate.getDate() + 1);
      wakeDate.setHours(hours, minutes, 0, 0);
      return wakeDate;
    } else if (currentTime < rule.wakeTime) {
      // We're in the morning portion — wake is today
      const wakeDate = new Date(d);
      wakeDate.setHours(hours, minutes, 0, 0);
      return wakeDate;
    }
  }
  
  return null;
};

/**
 * Compute the next occurrence of the sleep time across all rules.
 * Used to schedule the next sleep alarm after a wake.
 * Returns { date, rule } for the next sleep moment.
 */
export const getNextSleepTime = (rules: ScreenScheduleRule[], now?: Date): { date: Date; rule: ScreenScheduleRule } | null => {
  const d = now || new Date();
  let earliest: { date: Date; rule: ScreenScheduleRule } | null = null;

  for (const rule of rules) {
    if (!rule.enabled || !isValidTime(rule.sleepTime)) continue;
    
    const nextSleep = getNextTimeOnScheduledDay(rule.sleepTime, rule.days, d, 0);
    if (nextSleep && (!earliest || nextSleep < earliest.date)) {
      earliest = { date: nextSleep, rule };
    }
  }

  return earliest;
};

/**
 * Find the next occurrence of a given "HH:MM" time on one of the scheduled days.
 * @param time "HH:MM" target
 * @param days array of day indices (0=Sun..6=Sat)
 * @param from reference date
 * @param startOffset how many days from 'from' to start checking (0 = today, 1 = tomorrow)
 */
const getNextTimeOnScheduledDay = (time: string, days: number[], from: Date, startOffset: number): Date | null => {
  const { hours, minutes } = parseTime(time);
  
  for (let offset = startOffset; offset <= 7; offset++) {
    const candidate = new Date(from);
    candidate.setDate(candidate.getDate() + offset);
    candidate.setHours(hours, minutes, 0, 0);
    
    if (candidate <= from) continue; // Must be in the future
    if (days.includes(candidate.getDay())) {
      return candidate;
    }
  }

  return null;
};
