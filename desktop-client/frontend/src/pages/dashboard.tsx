import type { Account } from '../models/account';

const accounts: Account[] = [
  {
    id: 'amazon-us-001',
    name: 'Amazon US',
    platform: 'Amazon',
    login: '',
    profileId: 'profile-001',
    status: 'active'
  },
  {
    id: 'shopify-geteen',
    name: 'Geteen Shopify',
    platform: 'Shopify',
    login: '',
    profileId: 'profile-002',
    status: 'active'
  }
];

export default function Dashboard() {
  return (
    <div>
      <h1>Ant Enterprise</h1>
      {accounts.map(account => (
        <div key={account.id}>
          <h3>{account.name}</h3>
          <span>{account.platform}</span>
          <button>打开</button>
        </div>
      ))}
    </div>
  );
}
