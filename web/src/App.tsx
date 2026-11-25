import React from "react"
import { useAuth } from "./lib/AuthContext"
import { UnlockView } from "./components/UnlockView"
import { DashboardView } from "./components/DashboardView"
import { AccountsView } from "./components/AccountsView"
import { LogOut, Wallet, LayoutDashboard } from "lucide-react"

export const App: React.FC = () => {
  const { isUnlocked, isLoading, lock } = useAuth()
  const [currentTab, setCurrentTab] = React.useState<"dashboard" | "accounts">("dashboard")

  if (isLoading) {
    return (
      <div className="flex h-screen items-center justify-center bg-background">
        <div className="h-8 w-8 animate-spin rounded-full border-4 border-primary border-t-transparent" />
      </div>
    )
  }

  if (!isUnlocked) {
    return <UnlockView />
  }

  return (
    <div className="min-h-screen flex flex-col bg-background selection:bg-primary selection:text-primary-foreground">
      {/* Header */}
      <header className="sticky top-0 z-40 border-b border-border bg-card/60 backdrop-blur">
        <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 h-16 flex items-center justify-between">
          <div className="flex items-center gap-3">
            <div className="w-9 h-9 rounded-xl bg-gradient-to-tr from-primary to-emerald-400 flex items-center justify-center font-bold text-white shadow-lg shadow-primary/20">
              F
            </div>
            <div>
              <span className="text-xl font-bold tracking-tight text-white">Finorama</span>
              <span className="text-xs text-muted-foreground ml-2 hidden sm:inline">KSEI Portfolio</span>
            </div>
          </div>

          <div className="flex items-center gap-3">
            <nav className="flex items-center gap-1 bg-secondary/50 p-1 rounded-xl border border-border">
              <button
                onClick={() => setCurrentTab("dashboard")}
                className={`flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium rounded-lg transition ${
                  currentTab === "dashboard"
                    ? "bg-primary text-white shadow-sm"
                    : "text-muted-foreground hover:text-white"
                }`}
              >
                <LayoutDashboard className="w-3.5 h-3.5" />
                <span>Dashboard</span>
              </button>
              <button
                onClick={() => setCurrentTab("accounts")}
                className={`flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium rounded-lg transition ${
                  currentTab === "accounts"
                    ? "bg-primary text-white shadow-sm"
                    : "text-muted-foreground hover:text-white"
                }`}
              >
                <Wallet className="w-3.5 h-3.5" />
                <span>Accounts</span>
              </button>
            </nav>

            <button
              onClick={() => lock()}
              className="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-rose-300 hover:text-rose-200 bg-rose-950/40 hover:bg-rose-900/50 border border-rose-800/50 rounded-lg transition"
            >
              <LogOut className="w-3.5 h-3.5" />
              <span>Lock Vault</span>
            </button>
          </div>
        </div>
      </header>

      {/* Main view */}
      <main className="flex-1 max-w-7xl w-full mx-auto px-4 sm:px-6 lg:px-8 py-8">
        {currentTab === "dashboard" ? (
          <DashboardView onManageAccounts={() => setCurrentTab("accounts")} />
        ) : (
          <AccountsView onBackToDashboard={() => setCurrentTab("dashboard")} />
        )}
      </main>

      {/* Footer */}
      <footer className="border-t border-border/80 py-6 text-center text-xs text-muted-foreground">
        Finorama
      </footer>
    </div>
  )
}
