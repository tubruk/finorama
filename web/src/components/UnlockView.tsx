import React, { useState } from "react"
import { useAuth } from "../lib/AuthContext"
import { ShieldCheck, KeyRound, AlertCircle } from "lucide-react"

export const UnlockView: React.FC = () => {
  const { vaultStatus, unlock, init } = useAuth()
  const isFirstTime = vaultStatus?.initialized === false

  const [passphrase, setPassphrase] = useState("")
  const [confirmPassphrase, setConfirmPassphrase] = useState("")
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError(null)
    setLoading(true)

    try {
      if (isFirstTime) {
        if (passphrase.length < 8) {
          throw new Error("Passphrase must be at least 8 characters")
        }
        if (passphrase !== confirmPassphrase) {
          throw new Error("Passphrases do not match")
        }
        await init(passphrase, confirmPassphrase)
      } else {
        await unlock(passphrase)
      }
    } catch (err: any) {
      setError(err?.message || "Failed to authenticate")
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center p-4 bg-background">
      <div className="w-full max-w-md space-y-6 rounded-2xl bg-card border border-border p-8 shadow-2xl">
        <div className="text-center space-y-2">
          <div className="mx-auto flex h-12 w-12 items-center justify-center rounded-2xl bg-primary/10 text-primary border border-primary/20">
            {isFirstTime ? <ShieldCheck className="h-6 w-6" /> : <KeyRound className="h-6 w-6" />}
          </div>
          <h1 className="text-2xl font-bold tracking-tight text-white">
            {isFirstTime ? "Setup Master Passphrase" : "Unlock Finorama Vault"}
          </h1>
          <p className="text-xs text-muted-foreground">
            {isFirstTime
              ? "Your master passphrase derives an AES-256-GCM key used to encrypt all AKSES KSEI credentials."
              : "Enter your master passphrase to decrypt stored broker credentials and resume operations."}
          </p>
        </div>

        {error && (
          <div className="flex items-center gap-2 p-3.5 rounded-xl bg-destructive/15 border border-destructive/40 text-xs text-rose-300">
            <AlertCircle className="w-4 h-4 shrink-0" />
            <span>{error}</span>
          </div>
        )}

        <form onSubmit={handleSubmit} className="space-y-4">
          <div className="space-y-1.5">
            <label className="block text-xs font-semibold uppercase tracking-wider text-muted-foreground">
              Master Passphrase
            </label>
            <input
              type="password"
              required
              value={passphrase}
              onChange={(e) => setPassphrase(e.target.value)}
              placeholder="••••••••••••"
              className="w-full px-3.5 py-2.5 rounded-xl bg-secondary/40 border border-border focus:border-primary focus:ring-1 focus:ring-primary text-white placeholder-slate-600 text-sm outline-none transition"
            />
          </div>

          {isFirstTime && (
            <div className="space-y-1.5">
              <label className="block text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                Confirm Passphrase
              </label>
              <input
                type="password"
                required
                value={confirmPassphrase}
                onChange={(e) => setConfirmPassphrase(e.target.value)}
                placeholder="••••••••••••"
                className="w-full px-3.5 py-2.5 rounded-xl bg-secondary/40 border border-border focus:border-primary focus:ring-1 focus:ring-primary text-white placeholder-slate-600 text-sm outline-none transition"
              />
            </div>
          )}

          <button
            type="submit"
            disabled={loading}
            className="w-full py-2.5 px-4 rounded-xl font-medium text-sm text-white bg-primary hover:bg-primary/90 shadow-lg shadow-primary/20 transition disabled:opacity-50"
          >
            {loading ? "Processing..." : isFirstTime ? "Initialize Master Vault" : "Unlock Vault"}
          </button>
        </form>
      </div>
    </div>
  )
}
