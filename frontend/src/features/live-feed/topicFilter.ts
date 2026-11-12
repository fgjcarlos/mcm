// Filter for the live Topics view (issue #357). A plain text is searched as a
// case-insensitive substring of the topic. A text containing `+` or `#` is an
// MQTT topic filter instead: `+` matches exactly one level, `#` the remaining
// levels (including none, so `a/#` also matches `a`). A wildcard must fill its
// whole level and `#` must be last; otherwise the filter matches nothing.

function isWildcardFilter(text: string) {
  return text.includes('+') || text.includes('#')
}

export function isTopicFilterActive(text: string) {
  return text.trim() !== ''
}

export function isTopicPatternValid(text: string) {
  const pattern = text.trim()
  if (!isWildcardFilter(pattern)) return true
  const levels = pattern.split('/')
  return levels.every((level, index) => {
    if (level.includes('#')) return level === '#' && index === levels.length - 1
    return !level.includes('+') || level === '+'
  })
}

function matchesMQTTFilter(topic: string, pattern: string) {
  if (!isTopicPatternValid(pattern)) return false
  // Topics starting with `$` (such as `$SYS`) are not matched by a leading
  // wildcard, as in MQTT.
  if (topic.startsWith('$') && (pattern.startsWith('+') || pattern.startsWith('#'))) return false
  const filterLevels = pattern.split('/')
  const topicLevels = topic.split('/')
  for (let index = 0; index < filterLevels.length; index += 1) {
    const level = filterLevels[index]
    if (level === '#') return true
    if (index >= topicLevels.length) return false
    if (level !== '+' && level !== topicLevels[index]) return false
  }
  return filterLevels.length === topicLevels.length
}

export function matchesTopicFilter(topic: string, text: string) {
  const filter = text.trim()
  if (filter === '') return true
  if (isWildcardFilter(filter)) return matchesMQTTFilter(topic, filter)
  return topic.toLowerCase().includes(filter.toLowerCase())
}
