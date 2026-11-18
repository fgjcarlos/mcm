import { describe, expect, it } from 'vitest'
import { deploymentLamp } from './deploymentLamp'

describe('deploymentLamp', () => {
  it.each([
    ['active_verified', 'verified'],
    ['applied', 'caution'],
    ['pending_activation', 'caution'],
    ['applying', 'caution'],
    ['saved', 'caution'],
    ['pending', 'caution'],
    ['rolled_back', 'caution'],
    ['failed', 'failed'],
    ['rollback_failed', 'failed'],
    ['something_new', 'caution'],
    ['', 'caution'],
    [undefined, 'caution'],
  ] as const)('maps %s to the %s lamp', (status, lamp) => {
    expect(deploymentLamp(status)).toBe(lamp)
  })
})
