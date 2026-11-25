import React, { createContext, useContext, useEffect, useState } from "react"
import { api, getAuthToken, removeAuthToken, setAuthToken, VaultStatus } from "./api"

interface AuthContextType {
  vaultStatus: VaultStatus | null
  isLoading: boolean
  isUnlocked: boolean
  unlock: (pass: string) => Promise<void>
  init: (pass: string, confirm: string) => Promise<void>
  lock: () => Promise<void>
  refreshStatus: () => Promise<void>
}

const AuthContext = createContext<AuthContextType | undefined>(undefined)

export const AuthProvider: React.FC<{ children: React.ReactNode }> = ({ children }) => {
  const [vaultStatus, setVaultStatus] = useState<VaultStatus | null>(null)
  const [isLoading, setIsLoading] = useState(true)

  const refreshStatus = async () => {
    try {
      const status = await api.getVaultStatus()
      setVaultStatus(status)
      if (!status.unlocked) {
        removeAuthToken()
      }
    } catch (e) {
      console.error("Failed to check vault status", e)
    } finally {
      setIsLoading(false)
    }
  }

  useEffect(() => {
    refreshStatus()
  }, [])

  const unlock = async (passphrase: string) => {
    const res = await api.unlockVault(passphrase)
    setAuthToken(res.token)
    await refreshStatus()
  }

  const init = async (passphrase: string, confirm: string) => {
    const res = await api.initVault(passphrase, confirm)
    setAuthToken(res.token)
    await refreshStatus()
  }

  const lock = async () => {
    try {
      await api.lockVault()
    } finally {
      removeAuthToken()
      await refreshStatus()
    }
  }

  const isUnlocked = Boolean(vaultStatus?.unlocked && getAuthToken())

  return (
    <AuthContext.Provider
      value={{
        vaultStatus,
        isLoading,
        isUnlocked,
        unlock,
        init,
        lock,
        refreshStatus,
      }}
    >
      {children}
    </AuthContext.Provider>
  )
}

export const useAuth = () => {
  const context = useContext(AuthContext)
  if (!context) {
    throw new Error("useAuth must be used within an AuthProvider")
  }
  return context
}
