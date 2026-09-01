export interface Account {
  id: string;
  name: string;
  platform: string;
  login: string;
  profileId: string;
  ownerId?: string;
  status: 'active' | 'offline' | 'error';
}
