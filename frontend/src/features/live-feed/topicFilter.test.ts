import { describe, expect, it } from 'vitest'
import { isTopicFilterActive, isTopicPatternValid, matchesTopicFilter } from './topicFilter'

describe('topic filter', () => {
  it('matches every topic when empty or blank', () => {
    expect(isTopicFilterActive('')).toBe(false)
    expect(isTopicFilterActive('   ')).toBe(false)
    expect(matchesTopicFilter('a/b', '')).toBe(true)
    expect(matchesTopicFilter('a/b', '  ')).toBe(true)
  })

  it('searches a substring without regard to case and ignores surrounding spaces', () => {
    expect(isTopicFilterActive(' Line-1 ')).toBe(true)
    expect(matchesTopicFilter('factory/line-1/temperature', ' Line-1 ')).toBe(true)
    expect(matchesTopicFilter('factory/line-2/temperature', 'line-1')).toBe(false)
  })

  it('matches + as exactly one level', () => {
    expect(matchesTopicFilter('factory/line-1/temperature', 'factory/+/temperature')).toBe(true)
    expect(matchesTopicFilter('factory/line-1/humidity', 'factory/+/temperature')).toBe(false)
    expect(matchesTopicFilter('factory/a/b/temperature', 'factory/+/temperature')).toBe(false)
    expect(matchesTopicFilter('factory/temperature', 'factory/+/temperature')).toBe(false)
    expect(matchesTopicFilter('a//c', 'a/+/c')).toBe(true)
  })

  it('matches # as the remaining levels, including the parent level', () => {
    expect(matchesTopicFilter('factory/line-1/temperature', 'factory/#')).toBe(true)
    expect(matchesTopicFilter('factory', 'factory/#')).toBe(true)
    expect(matchesTopicFilter('factoryx/a', 'factory/#')).toBe(false)
    expect(matchesTopicFilter('any/topic', '#')).toBe(true)
    expect(matchesTopicFilter('factory/line-1/temperature', 'factory/+/#')).toBe(true)
  })

  it('treats a wildcard filter as an exact, case-sensitive MQTT match', () => {
    expect(matchesTopicFilter('Factory/line-1', 'factory/+')).toBe(false)
    expect(matchesTopicFilter('x/factory/line-1', 'factory/+')).toBe(false)
  })

  it('does not match topics starting with $ from a leading wildcard', () => {
    expect(matchesTopicFilter('$SYS/broker/uptime', '#')).toBe(false)
    expect(matchesTopicFilter('$SYS/broker/uptime', '+/broker/uptime')).toBe(false)
    expect(matchesTopicFilter('$SYS/broker/uptime', '$SYS/#')).toBe(true)
  })

  it('matches nothing when a wildcard does not fill its level or # is not last', () => {
    for (const pattern of ['factory/#/temperature', 'factory#', 'factory/line+', 'a/b#']) {
      expect(isTopicPatternValid(pattern)).toBe(false)
      expect(matchesTopicFilter('factory/line-1/temperature', pattern)).toBe(false)
    }
    expect(isTopicPatternValid('factory/+/#')).toBe(true)
    expect(isTopicPatternValid('plain text')).toBe(true)
  })
})
