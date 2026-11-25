import React, { useState } from "react"
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query"
import { api, Account } from "../lib/api"
import { Trash2, RefreshCw, ArrowLeft, AlertCircle } from "lucide-react"

export const AccountsView: React.FC<{ onBackToDashboard: () => void }> = ({ onBackToDashboard }) => {
  const queryClient = useQueryClient()

  const [name, setName] = useState("")
  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")
  const [formError, setFormError] = useState<string | null>(null)

  const { data, isLoading } = useQuery({
    queryKey: ["accounts"],
    queryFn: () => api.getAccounts(),
  })

  const addMutation = useMutation({
    mutationFn: () => api.addAccount({ name, username, password }),
    onSuccess: () => {
      setName("")
      setUsername("")
      setPassword("")
      setFormError(null)
      queryClient.invalidateQueries({ queryKey: ["accounts"] })
      queryClient.invalidateQueries({ queryKey: ["portfolio"] })
    },
    onError: (err: any) => {
      setFormError(err?.message || "Failed to add account")
    },
  })

  const deleteMutation = useMutation({
    mutationFn: (id: number) => api.deleteAccount(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["accounts"] })
      queryClient.invalidateQueries({ queryKey: ["portfolio"] })
    },
  })

  const syncMutation = useMutation({
    mutationFn: (id: number) => api.triggerSync(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["syncStatus"] })
    },
  })

  const handleAddSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    setFormError(null)
    addMutation.mutate()
  }

  return (
    <div className="space-y-8">
      <div className="flex items-center justify-between pb-2 border-b border-border">
        <div>
          <h1 className="text-2xl font-bold tracking-tight text-white">Connected KSEI Accounts</h1>
          <p className="text-sm text-muted-foreground">Manage your AKSES KSEI credentials safely encrypted in local SQLite</p>
        </div>
        <button
          onClick={onBackToDashboard}
          className="flex items-center gap-1.5 px-3.5 py-2 rounded-xl text-sm font-medium bg-secondary hover:bg-secondary/80 text-foreground border border-border transition"
        >
          <ArrowLeft className="w-4 h-4" />
          <span>Back to Dashboard</span>
        </button>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-3 gap-8">
        {/* Add Account Card */}
        <div className="bg-card/80 border border-border rounded-2xl p-6 shadow-xl h-fit">
          <h2 className="text-lg font-bold text-white mb-2">Connect New Account</h2>
          <p className="text-xs text-muted-foreground mb-5">
            Passwords are encrypted in SQLite with AES-256-GCM. Unencrypted passwords are never stored.
          </p>

          {formError && (
            <div className="mb-4 flex items-center gap-2 p-3 rounded-xl bg-destructive/15 border border-destructive/40 text-xs text-rose-300">
              <AlertCircle className="w-4 h-4 shrink-0" />
              <span>{formError}</span>
            </div>
          )}

          <form onSubmit={handleAddSubmit} className="space-y-4">
            <div>
              <label className="block text-xs font-semibold uppercase tracking-wider text-muted-foreground mb-1.5">
                Account Label
              </label>
              <input
                type="text"
                required
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="e.g. Primary Account"
                className="w-full px-3.5 py-2 rounded-xl bg-secondary/40 border border-border focus:border-primary focus:ring-1 focus:ring-primary text-white placeholder-slate-600 text-sm outline-none transition"
              />
            </div>

            <div>
              <label className="block text-xs font-semibold uppercase tracking-wider text-muted-foreground mb-1.5">
                AKSES KSEI Username / Email
              </label>
              <input
                type="email"
                required
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                placeholder="name@domain.com"
                className="w-full px-3.5 py-2 rounded-xl bg-secondary/40 border border-border focus:border-primary focus:ring-1 focus:ring-primary text-white placeholder-slate-600 text-sm outline-none transition"
              />
            </div>

            <div>
              <label className="block text-xs font-semibold uppercase tracking-wider text-muted-foreground mb-1.5">
                AKSES KSEI Password
              </label>
              <input
                type="password"
                required
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder="••••••••"
                className="w-full px-3.5 py-2 rounded-xl bg-secondary/40 border border-border focus:border-primary focus:ring-1 focus:ring-primary text-white placeholder-slate-600 text-sm outline-none transition"
              />
            </div>

            <button
              type="submit"
              disabled={addMutation.isPending}
              className="w-full py-2.5 px-4 rounded-xl font-medium text-sm text-white bg-primary hover:bg-primary/90 shadow-lg shadow-primary/20 transition disabled:opacity-50"
            >
              {addMutation.isPending ? "Connecting..." : "Save Encrypted Account"}
            </button>
          </form>
        </div>

        {/* Accounts List */}
        <div className="lg:col-span-2 bg-card/80 border border-border rounded-2xl overflow-hidden shadow-xl">
          <div className="px-6 py-4 border-b border-border">
            <h2 className="text-lg font-bold text-white">Configured Accounts ({data?.accounts?.length || 0})</h2>
          </div>
          <div className="divide-y divide-border/60">
            {isLoading ? (
              <div className="py-12 text-center text-muted-foreground">Loading accounts...</div>
            ) : data?.accounts && data.accounts.length > 0 ? (
              data.accounts.map((acc: Account) => (
                <div key={acc.id} className="p-5 flex flex-col sm:flex-row items-start sm:items-center justify-between gap-4">
                  <div>
                    <div className="flex items-center gap-2">
                      <span className="font-bold text-white">{acc.name}</span>
                      <span className="text-xs px-2 py-0.5 rounded-full bg-secondary text-muted-foreground font-mono">
                        {acc.username}
                      </span>
                    </div>
                    <div className="text-xs text-muted-foreground mt-1">
                      Last sync: {acc.last_synced_at ? new Date(acc.last_synced_at).toLocaleString("id-ID") : "Never"}
                    </div>
                    {acc.last_sync_error && (
                      <div className="text-xs text-rose-400 mt-1">Error: {acc.last_sync_error}</div>
                    )}
                  </div>
                  <div className="flex items-center gap-2">
                    <button
                      onClick={() => syncMutation.mutate(acc.id)}
                      disabled={syncMutation.isPending}
                      className="flex items-center gap-1 px-3 py-1.5 rounded-lg text-xs font-medium bg-primary/10 text-primary hover:bg-primary/20 border border-primary/20 transition"
                    >
                      <RefreshCw className="w-3.5 h-3.5" />
                      <span>Sync Now</span>
                    </button>
                    <button
                      onClick={() => {
                        if (confirm(`Delete account '${acc.name}' and all associated holdings history?`)) {
                          deleteMutation.mutate(acc.id)
                        }
                      }}
                      className="p-1.5 rounded-lg text-xs text-rose-400 hover:bg-rose-950/40 border border-rose-800/40 transition"
                      title="Delete Account"
                    >
                      <Trash2 className="w-4 h-4" />
                    </button>
                  </div>
                </div>
              ))
            ) : (
              <div className="p-8 text-center text-muted-foreground">
                No accounts added yet. Use the form on the left to connect your first KSEI account.
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}
