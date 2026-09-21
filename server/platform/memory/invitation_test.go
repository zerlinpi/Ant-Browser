package memory

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	authservice "github.com/zerlinpi/Ant-Browser/server/services/auth-service"
	memberservice "github.com/zerlinpi/Ant-Browser/server/services/member-service"
	workspaceservice "github.com/zerlinpi/Ant-Browser/server/services/workspace-service"
)

func invitationFixture(t *testing.T) (*Store, *workspaceservice.Service, workspaceservice.Workspace, workspaceservice.Invitation) {
	t.Helper()
	ctx := context.Background()
	store := New()
	for _, id := range []string{"owner", "invitee"} {
		if err := store.CreateUser(ctx, authservice.User{ID: id, Email: id + "@example.com", Status: "active"}); err != nil {
			t.Fatal(err)
		}
	}
	service := workspaceservice.New(store)
	workspace, err := service.Create(ctx, "owner", workspaceservice.CreateInput{Name: "Invite test"})
	if err != nil {
		t.Fatal(err)
	}
	invitation, err := service.Invite(ctx, "owner", workspace.ID, workspaceservice.InviteInput{Email: "invitee@example.com", Role: memberservice.RoleOperator})
	if err != nil {
		t.Fatal(err)
	}
	if store.invitations[invitation.ID].Token != "" {
		t.Fatal("plaintext token was persisted")
	}
	return store, service, workspace, invitation
}

func TestInvitationRejectsInactiveIdentityAndTenant(t *testing.T) {
	for _, scenario := range []string{"expired", "disabled_user", "changed_email", "archived_workspace", "suspended_org"} {
		t.Run(scenario, func(t *testing.T) {
			store, service, workspace, invitation := invitationFixture(t)
			switch scenario {
			case "expired":
				item := store.invitations[invitation.ID]
				item.ExpiresAt = time.Now().Add(-time.Second)
				store.invitations[item.ID] = item
			case "disabled_user", "changed_email":
				user := store.users["invitee"]
				if scenario == "disabled_user" {
					user.Status = "suspended"
				} else {
					user.Email = "other@example.com"
				}
				store.users[user.ID] = user
			case "archived_workspace":
				workspace.Status = "archived"
				store.workspaces[workspace.ID] = workspace
			case "suspended_org":
				org := store.organizations[workspace.OrganizationID]
				org.Status = "suspended"
				store.organizations[org.ID] = org
			}
			if _, err := service.Accept(context.Background(), "invitee", workspace.ID, invitation.Token); err == nil {
				t.Fatal("invalid acceptance succeeded")
			}
			if store.invitations[invitation.ID].Status != "pending" {
				t.Fatal("failed acceptance consumed token")
			}
			if _, exists := store.memberships[membershipKey(workspace.ID, "invitee")]; exists {
				t.Fatal("failed acceptance created membership")
			}
		})
	}
}

func TestInvitationConcurrentConsumptionIsSingleUse(t *testing.T) {
	_, service, workspace, invitation := invitationFixture(t)
	var group sync.WaitGroup
	results := make(chan error, 16)
	for i := 0; i < cap(results); i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := service.Accept(context.Background(), "invitee", workspace.ID, invitation.Token)
			results <- err
		}()
	}
	group.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, workspaceservice.ErrInvitationUsed) {
			t.Fatalf("unexpected acceptance error: %v", err)
		}
	}
	if success != 1 {
		t.Fatalf("got %d successful acceptances", success)
	}
}
