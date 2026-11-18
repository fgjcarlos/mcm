import { useState, type ReactNode } from 'react'
import { RecentMFAContext, type Proof } from './useRecentMFA'

// Mounted around the signed-in dashboard only, so logout (which unmounts it) discards the proof.
export default function RecentMFAProvider({ children }: { children: ReactNode }) {
  return <RecentMFAContext.Provider value={useState<Proof | null>(null)}>{children}</RecentMFAContext.Provider>
}
