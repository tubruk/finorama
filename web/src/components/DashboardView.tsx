import React from "react"
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query"
import { api, Holding } from "../lib/api"
import { formatIDR } from "../lib/utils"
import { RefreshCw } from "lucide-react"
import {
  ResponsiveContainer,
  AreaChart,
  Area,
  XAxis,
  YAxis,
  Tooltip,
  CartesianGrid,
} from "recharts"

export const DashboardView: React.FC<{ onManageAccounts: () => void }> = ({ onManageAccounts }) => {
  const queryClient = useQueryClient()

  const { data: overview, isLoading, error } = useQuery({
    queryKey: ["portfolio"],
    queryFn: () => api.getPortfolio(),
  })

  const { data: syncStatus } = useQuery({
    queryKey: ["syncStatus"],
    queryFn: () => api.getSyncStatus(),
    refetchInterval: (query) => (query.state.data?.is_syncing ? 2000 : 15000),
  })

  const syncMutation = useMutation({
    mutationFn: (accountId?: number) => api.triggerSync(accountId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["syncStatus"] })
      setTimeout(() => {
        queryClient.invalidateQueries({ queryKey: ["portfolio"] })
      }, 3000)
    },
  })

  const isSyncing = Boolean(syncStatus?.is_syncing || syncMutation.isPending)

  if (isLoading) {
    return (
      <div className="py-24 text-center">
        <div className="h-8 w-8 animate-spin rounded-full border-4 border-primary border-t-transparent mx-auto" />
      </div>
    )
  }

  if (error) {
    return (
      <div className="p-4 rounded-xl bg-destructive/15 border border-destructive/30 text-rose-300 text-sm">
        Failed to load portfolio overview: {(error as any).message}
      </div>
    )
  }

  return (
    <div className="space-y-8">
      {/* Action Bar */}
      <div className="flex flex-col sm:flex-row items-start sm:items-center justify-between gap-4 pb-2 border-b border-border">
        <div>
          <h1 className="text-2xl font-bold tracking-tight text-white">Portfolio Overview</h1>
          <p className="text-sm text-muted-foreground">Aggregated snapshot across all connected KSEI accounts</p>
        </div>
        <div className="flex items-center gap-3">
          <button
            onClick={() => syncMutation.mutate(undefined)}
            disabled={isSyncing}
            className="inline-flex items-center gap-2 px-4 py-2 rounded-xl text-sm font-medium bg-primary hover:bg-primary/90 text-white shadow-lg shadow-primary/20 transition disabled:opacity-50"
          >
            <RefreshCw className={`w-4 h-4 ${isSyncing ? "animate-spin" : ""}`} />
            <span>{isSyncing ? "Syncing..." : "Trigger Sync Now"}</span>
          </button>
          <button
            onClick={onManageAccounts}
            className="px-3.5 py-2 rounded-xl text-sm font-medium bg-secondary hover:bg-secondary/80 text-foreground border border-border transition"
          >
            Manage Accounts
          </button>
        </div>
      </div>

      {/* Metric Cards Grid */}
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-5 gap-4">
        <div className="bg-gradient-to-br from-primary/20 to-card border border-primary/30 rounded-2xl p-5 shadow-lg relative overflow-hidden sm:col-span-2 lg:col-span-1">
          <div className="text-xs font-semibold uppercase tracking-wider text-primary">Total Portfolio</div>
          <div className="text-2xl font-bold text-white mt-2">{formatIDR(overview?.total_value || 0)}</div>
          <div className="text-xs text-muted-foreground mt-1">{overview?.accounts?.length || 0} Active Account(s)</div>
        </div>

        <div className="bg-card/80 border border-border rounded-2xl p-5">
          <div className="flex items-center justify-between text-xs font-semibold uppercase tracking-wider text-muted-foreground">
            <span>Saham (Equity)</span>
            <span className="w-2.5 h-2.5 rounded-full bg-emerald-400"></span>
          </div>
          <div className="text-xl font-bold text-white mt-2">{formatIDR(overview?.total_equity || 0)}</div>
        </div>

        <div className="bg-card/80 border border-border rounded-2xl p-5">
          <div className="flex items-center justify-between text-xs font-semibold uppercase tracking-wider text-muted-foreground">
            <span>Reksadana</span>
            <span className="w-2.5 h-2.5 rounded-full bg-sky-400"></span>
          </div>
          <div className="text-xl font-bold text-white mt-2">{formatIDR(overview?.total_mutual_fund || 0)}</div>
        </div>

        <div className="bg-card/80 border border-border rounded-2xl p-5">
          <div className="flex items-center justify-between text-xs font-semibold uppercase tracking-wider text-muted-foreground">
            <span>Obligasi / SBN</span>
            <span className="w-2.5 h-2.5 rounded-full bg-amber-400"></span>
          </div>
          <div className="text-xl font-bold text-white mt-2">{formatIDR(overview?.total_bond || 0)}</div>
        </div>

        <div className="bg-card/80 border border-border rounded-2xl p-5">
          <div className="flex items-center justify-between text-xs font-semibold uppercase tracking-wider text-muted-foreground">
            <span>Cash RDN</span>
            <span className="w-2.5 h-2.5 rounded-full bg-purple-400"></span>
          </div>
          <div className="text-xl font-bold text-white mt-2">{formatIDR(overview?.total_cash || 0)}</div>
        </div>
      </div>

      {/* Historical Valuation (Timeseries Chart) */}
      <div className="bg-card/80 border border-border rounded-2xl p-6 shadow-xl space-y-4">
        <div className="flex items-center justify-between">
          <h2 className="text-lg font-bold text-white">Historical Valuation Timeline</h2>
          <span className="text-xs text-muted-foreground">Daily Aggregate</span>
        </div>
        {overview?.historical && overview.historical.length > 0 ? (
          <div className="w-full h-72 pt-4">
            <ResponsiveContainer width="100%" height="100%">
              <AreaChart data={overview.historical}>
                <defs>
                  <linearGradient id="valGrad" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="5%" stopColor="#6366f1" stopOpacity={0.4} />
                    <stop offset="95%" stopColor="#6366f1" stopOpacity={0.0} />
                  </linearGradient>
                </defs>
                <CartesianGrid strokeDasharray="3 3" stroke="#1e293b" vertical={false} />
                <XAxis
                  dataKey="date"
                  stroke="#64748b"
                  fontSize={12}
                  tickLine={false}
                  axisLine={{ stroke: "#334155" }}
                  tickFormatter={(val) => {
                    const parts = val.split("-")
                    return parts.length === 3 ? `${parts[2]}/${parts[1]}` : val
                  }}
                />
                <YAxis
                  stroke="#64748b"
                  fontSize={12}
                  tickLine={false}
                  axisLine={{ stroke: "#334155" }}
                  tickFormatter={(v) => {
                    if (v >= 1e9) return (v / 1e9).toFixed(1) + "B"
                    if (v >= 1e6) return (v / 1e6).toFixed(1) + "M"
                    return v.toString()
                  }}
                />
                <Tooltip
                  content={({ active, payload }) => {
                    if (active && payload && payload.length) {
                      const data = payload[0].payload
                      return (
                        <div className="rounded-xl border border-border bg-card p-3 shadow-xl">
                          <p className="text-xs text-muted-foreground">{data.date}</p>
                          <p className="text-sm font-bold text-white">{formatIDR(data.total_value)}</p>
                        </div>
                      )
                    }
                    return null
                  }}
                />
                <Area
                  type="monotone"
                  dataKey="total_value"
                  stroke="#6366f1"
                  strokeWidth={2}
                  fillOpacity={1}
                  fill="url(#valGrad)"
                />
              </AreaChart>
            </ResponsiveContainer>
          </div>
        ) : (
          <div className="py-12 text-center text-xs text-muted-foreground">
            No historical capture data yet. Sync accounts to build historical valuation.
          </div>
        )}
      </div>

      {/* Holdings Table */}
      <div className="bg-card/80 border border-border rounded-2xl overflow-hidden shadow-xl">
        <div className="px-6 py-4 border-b border-border flex items-center justify-between">
          <h2 className="text-lg font-bold text-white">Current Holdings ({overview?.holdings?.length || 0})</h2>
        </div>
        <div className="overflow-x-auto">
          <table className="w-full text-left text-sm">
            <thead className="bg-secondary/40 text-xs font-semibold uppercase tracking-wider text-muted-foreground border-b border-border">
              <tr>
                <th className="px-6 py-3.5">Asset</th>
                <th className="px-6 py-3.5">Type</th>
                <th className="px-6 py-3.5">Custodian / Account</th>
                <th className="px-6 py-3.5 text-right">Quantity</th>
                <th className="px-6 py-3.5 text-right">Price (IDR)</th>
                <th className="px-6 py-3.5 text-right">Total (IDR)</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border/60">
              {overview?.holdings && overview.holdings.length > 0 ? (
                overview.holdings.map((h: Holding) => (
                  <tr key={h.id} className="hover:bg-secondary/20 transition">
                    <td className="px-6 py-4">
                      <div className="font-bold text-white">{h.security_code}</div>
                      <div className="text-xs text-muted-foreground truncate max-w-xs">{h.security_name}</div>
                    </td>
                    <td className="px-6 py-4">
                      <span className="inline-flex px-2 py-0.5 text-xs font-medium rounded-md bg-secondary text-primary border border-border">
                        {h.asset_type}
                      </span>
                    </td>
                    <td className="px-6 py-4 text-xs">
                      <div className="text-foreground">{h.account_name}</div>
                      <div className="text-muted-foreground">{h.participant}</div>
                    </td>
                    <td className="px-6 py-4 text-right font-mono">{h.quantity.toLocaleString("id-ID")}</td>
                    <td className="px-6 py-4 text-right font-mono text-muted-foreground">
                      {h.price.toLocaleString("id-ID")}
                    </td>
                    <td className="px-6 py-4 text-right font-mono font-bold text-white">
                      {formatIDR(h.total_value)}
                    </td>
                  </tr>
                ))
              ) : (
                <tr>
                  <td colSpan={6} className="px-6 py-8 text-center text-muted-foreground">
                    No holdings captured yet. Trigger a sync after connecting accounts.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  )
}
