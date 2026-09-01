package account

// Account represents an ecommerce account bound to a browser profile.
// Examples: Amazon seller account, Shopify store, TikTok shop.

type Account struct {
	ID string `json:"id"`
	Name string `json:"name"`
	Platform string `json:"platform"`
	Login string `json:"login"`
	ProfileID string `json:"profile_id"`
	OwnerID string `json:"owner_id"`
	Status string `json:"status"`
}

// AccountManager manages available accounts.
type AccountManager struct {
	accounts map[string]Account
}

func NewAccountManager() *AccountManager {
	return &AccountManager{
		accounts: make(map[string]Account),
	}
}

func (m *AccountManager) Add(account Account) {
	m.accounts[account.ID] = account
}

func (m *AccountManager) Get(id string) (Account, bool) {
	a, ok := m.accounts[id]
	return a, ok
}
