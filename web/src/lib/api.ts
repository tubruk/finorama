export interface VaultStatus {
  initialized: boolean
  unlocked: boolean
}

export interface Account {
  id: number
  name: string
  username: string
  is_active: boolean
  last_synced_at?: string
  last_sync_error: string
  created_at: string
}

export interface Holding {
  id: number
  account_id: number
  account_name?: string
  captured_at: string
  captured_date: string
  asset_type: "EQUITY" | "MUTUAL_FUND" | "BOND" | "CASH" | string
  security_code: string
  security_name: string
  participant: string
  currency: string
  quantity: number
  price: number
  total_value: number
}

export interface DailyAggregate {
  date: string
  total_value: number
}

export interface PortfolioOverview {
  total_value: number
  total_equity: number
  total_mutual_fund: number
  total_bond: number
  total_cash: number
  total_other: number
  accounts: {
    account_id: number
    account_name: string
    username: string
    total_value: number
    last_synced_at?: string
  }[]
  holdings: Holding[]
  historical: DailyAggregate[]
}

const TOKEN_KEY = "finorama_auth_token"

export const getAuthToken = (): string | null => {
  return localStorage.getItem(TOKEN_KEY)
}

export const setAuthToken = (token: string) => {
  localStorage.setItem(TOKEN_KEY, token)
}

export const removeAuthToken = () => {
  localStorage.removeItem(TOKEN_KEY)
}

async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
  const token = getAuthToken()
  const headers = new Headers(options.headers || {})
  headers.set("Content-Type", "application/json")

  if (token) {
    headers.set("Authorization", `Bearer ${token}`)
  }

  const response = await fetch(`/api/v1${path}`, {
    ...options,
    headers,
  })

  if (!response.ok) {
    const errorBody = await response.json().catch(() => ({}))
    const errorMsg = errorBody?.error?.message || `Request failed with status ${response.status}`
    throw new Error(errorMsg)
  }

  return response.json()
}

export const api = {
  getVaultStatus: () => request<VaultStatus>("/vault/status"),
  initVault: (passphrase: string, confirm_passphrase: string) =>
    request<{ token: string; message: string }>("/vault/init", {
      method: "POST",
      body: JSON.stringify({ passphrase, confirm_passphrase }),
    }),
  unlockVault: (passphrase: string) =>
    request<{ token: string; message: string }>("/vault/unlock", {
      method: "POST",
      body: JSON.stringify({ passphrase }),
    }),
  lockVault: () =>
    request<{ message: string }>("/vault/lock", {
      method: "POST",
    }),
  getPortfolio: () => request<PortfolioOverview>("/portfolio"),
  getAccounts: () => request<{ accounts: Account[] }>("/accounts"),
  addAccount: (data: { name: string; username: string; password: string }) =>
    request<Account>("/accounts", {
      method: "POST",
      body: JSON.stringify(data),
    }),
  deleteAccount: (id: number) =>
    request<{ message: string }>(`/accounts/${id}`, {
      method: "DELETE",
    }),
  triggerSync: (accountId?: number) => {
    const query = accountId ? `?account_id=${accountId}` : ""
    return request<{ status: string; message: string }>(`/sync${query}`, {
      method: "POST",
    })
  },
  getSyncStatus: () => request<{ is_syncing: boolean }>("/sync/status"),
}
