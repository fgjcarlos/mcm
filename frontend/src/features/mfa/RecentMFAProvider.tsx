import { useState, type ReactNode } from 'react'
import { MFAEnrollmentContext, RecentMFAContext, type Proof } from './useRecentMFA'

// Mounted around the signed-in dashboard only, so logout (which unmounts it) discards the proof.
export default function RecentMFAProvider({ children, mfaEnabled }: { children: ReactNode; mfaEnabled?: boolean }) {
  return (
    <RecentMFAContext.Provider value={useState<Proof | null>(null)}>
      <MFAEnrollmentContext.Provider value={mfaEnabled}>{children}</MFAEnrollmentContext.Provider>
    </RecentMFAContext.Provider>
  )
}
