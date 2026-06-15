import { describe, expect, it } from 'vitest'
import { can, permissionTitle, requiredRoleFor } from './permissions'

describe('can()', () => {
  describe('viewer role', () => {
    it('cannot perform acl.write', () => {
      expect(can('viewer', 'acl.write')).toBe(false)
    })

    it('cannot perform mqttUser.write', () => {
      expect(can('viewer', 'mqttUser.write')).toBe(false)
    })

    it('cannot perform deploy.preview', () => {
      expect(can('viewer', 'deploy.preview')).toBe(false)
    })

    it('cannot perform deploy.apply', () => {
      expect(can('viewer', 'deploy.apply')).toBe(false)
    })

    it('cannot perform adminUser.write', () => {
      expect(can('viewer', 'adminUser.write')).toBe(false)
    })
  })

  describe('auditor role', () => {
    it('cannot perform acl.write', () => {
      expect(can('auditor', 'acl.write')).toBe(false)
    })

    it('cannot perform mqttUser.write', () => {
      expect(can('auditor', 'mqttUser.write')).toBe(false)
    })

    it('cannot perform deploy.preview', () => {
      expect(can('auditor', 'deploy.preview')).toBe(false)
    })

    it('cannot perform deploy.apply', () => {
      expect(can('auditor', 'deploy.apply')).toBe(false)
    })

    it('cannot perform adminUser.write', () => {
      expect(can('auditor', 'adminUser.write')).toBe(false)
    })
  })

  describe('operator role', () => {
    it('can perform acl.write', () => {
      expect(can('operator', 'acl.write')).toBe(true)
    })

    it('can perform mqttUser.write', () => {
      expect(can('operator', 'mqttUser.write')).toBe(true)
    })

    it('can perform deploy.preview', () => {
      expect(can('operator', 'deploy.preview')).toBe(true)
    })

    it('cannot perform deploy.apply (requires admin)', () => {
      expect(can('operator', 'deploy.apply')).toBe(false)
    })

    it('cannot perform adminUser.write (requires admin)', () => {
      expect(can('operator', 'adminUser.write')).toBe(false)
    })
  })

  describe('admin role', () => {
    it('can perform acl.write', () => {
      expect(can('admin', 'acl.write')).toBe(true)
    })

    it('can perform mqttUser.write', () => {
      expect(can('admin', 'mqttUser.write')).toBe(true)
    })

    it('can perform deploy.preview', () => {
      expect(can('admin', 'deploy.preview')).toBe(true)
    })

    it('can perform deploy.apply', () => {
      expect(can('admin', 'deploy.apply')).toBe(true)
    })

    it('can perform adminUser.write', () => {
      expect(can('admin', 'adminUser.write')).toBe(true)
    })
  })

  describe('unknown/empty role (escape hatch)', () => {
    it('denies acl.write for an unknown string role', () => {
      expect(can('superuser', 'acl.write')).toBe(false)
    })

    it('denies deploy.apply for an empty string role', () => {
      expect(can('', 'deploy.apply')).toBe(false)
    })

    it('denies all actions for an unknown role', () => {
      expect(can('unknown-role', 'mqttUser.write')).toBe(false)
      expect(can('unknown-role', 'deploy.preview')).toBe(false)
      expect(can('unknown-role', 'adminUser.write')).toBe(false)
    })
  })

  // FIX 5: undefined role must always return false (safe default deny)
  describe('undefined role (safe default deny)', () => {
    it('denies acl.write for undefined role', () => {
      expect(can(undefined as unknown as string, 'acl.write')).toBe(false)
    })

    it('denies mqttUser.write for undefined role', () => {
      expect(can(undefined as unknown as string, 'mqttUser.write')).toBe(false)
    })

    it('denies deploy.preview for undefined role', () => {
      expect(can(undefined as unknown as string, 'deploy.preview')).toBe(false)
    })

    it('denies deploy.apply for undefined role', () => {
      expect(can(undefined as unknown as string, 'deploy.apply')).toBe(false)
    })

    it('denies adminUser.write for undefined role', () => {
      expect(can(undefined as unknown as string, 'adminUser.write')).toBe(false)
    })
  })
})

describe('requiredRoleFor()', () => {
  it('returns operator for acl.write', () => {
    expect(requiredRoleFor('acl.write')).toBe('operator')
  })

  it('returns operator for mqttUser.write', () => {
    expect(requiredRoleFor('mqttUser.write')).toBe('operator')
  })

  it('returns operator for deploy.preview', () => {
    expect(requiredRoleFor('deploy.preview')).toBe('operator')
  })

  it('returns admin for deploy.apply', () => {
    expect(requiredRoleFor('deploy.apply')).toBe('admin')
  })

  it('returns admin for adminUser.write', () => {
    expect(requiredRoleFor('adminUser.write')).toBe('admin')
  })
})

describe('permissionTitle()', () => {
  // FIX 7: sub-admin floors keep "or higher"; top-tier (admin) drops it
  it('returns "or higher" for sub-admin actions (operator floor)', () => {
    expect(permissionTitle('acl.write')).toBe('Requires operator role or higher')
  })

  it('returns "or higher" for operator-floor deploy.preview', () => {
    expect(permissionTitle('deploy.preview')).toBe('Requires operator role or higher')
  })

  it('returns "or higher" for operator-floor mqttUser.write', () => {
    expect(permissionTitle('mqttUser.write')).toBe('Requires operator role or higher')
  })

  it('drops "or higher" for admin-floor actions (top tier)', () => {
    expect(permissionTitle('deploy.apply')).toBe('Requires admin role')
  })

  it('drops "or higher" for admin-floor adminUser.write', () => {
    expect(permissionTitle('adminUser.write')).toBe('Requires admin role')
  })
})
