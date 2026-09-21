package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	proxyservice "github.com/zerlinpi/Ant-Browser/server/services/proxy-service"
)

const proxyColumns = `p.id::text, p.workspace_id::text, p.name, p.protocol, p.host, p.port,
COALESCE(p.username, ''), p.has_credentials, COALESCE(p.secret_ref, ''), p.connector_type,
p.kernel, p.status, p.version, COALESCE(p.created_by::text, ''), p.created_at, p.updated_at, p.deleted_at`

func (s *Store) CreateProxy(ctx context.Context, proxy proxyservice.Proxy) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO proxies
		(id, workspace_id, name, protocol, host, port, username, has_credentials, secret_ref,
		 connector_type, kernel, status, version, created_by, created_at, updated_at, deleted_at)
		VALUES ($1::uuid,$2::uuid,$3,$4,$5,$6,$7,$8,NULLIF($9,''),$10,$11,$12,$13,NULLIF($14,'')::uuid,$15,$16,$17)`,
		proxy.ID, proxy.WorkspaceID, proxy.Name, proxy.Protocol, proxy.Host, proxy.Port, proxy.Username,
		proxy.HasCredentials, proxy.SecretRef, proxy.ConnectorType, proxy.Kernel, proxy.Status, proxy.Version,
		proxy.CreatedBy, proxy.CreatedAt, proxy.UpdatedAt, proxy.DeletedAt)
	if isUniqueViolation(err) {
		return errors.New("proxy name already exists")
	}
	return err
}

func (s *Store) FindProxy(ctx context.Context, workspaceID, proxyID string) (proxyservice.Proxy, error) {
	return scanProxy(s.pool.QueryRow(ctx, `SELECT `+proxyColumns+` FROM proxies p WHERE p.workspace_id=$1::uuid AND p.id=$2::uuid AND p.deleted_at IS NULL`, workspaceID, proxyID))
}

func (s *Store) ListProxies(ctx context.Context, workspaceID string) ([]proxyservice.Proxy, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+proxyColumns+` FROM proxies p WHERE p.workspace_id=$1::uuid AND p.deleted_at IS NULL ORDER BY p.created_at,p.id`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]proxyservice.Proxy, 0)
	for rows.Next() {
		item, scanErr := scanProxy(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) UpdateProxy(ctx context.Context, proxy proxyservice.Proxy, expectedVersion int64) (proxyservice.Proxy, error) {
	item, err := scanProxy(s.pool.QueryRow(ctx, `UPDATE proxies p SET name=$4, protocol=$5, host=$6, port=$7,
	username=$8, has_credentials=$9, secret_ref=NULLIF($10,''), connector_type=$11, kernel=$12,
	status=$13, updated_at=$14, version=p.version+1 WHERE p.workspace_id=$1::uuid AND p.id=$2::uuid AND p.version=$3 AND p.deleted_at IS NULL RETURNING `+proxyColumns,
		proxy.WorkspaceID, proxy.ID, expectedVersion, proxy.Name, proxy.Protocol, proxy.Host, proxy.Port, proxy.Username,
		proxy.HasCredentials, proxy.SecretRef, proxy.ConnectorType, proxy.Kernel, proxy.Status, proxy.UpdatedAt))
	if errors.Is(err, proxyservice.ErrNotFound) {
		if _, findErr := s.FindProxy(ctx, proxy.WorkspaceID, proxy.ID); findErr == nil {
			return proxyservice.Proxy{}, proxyservice.ErrVersionConflict
		}
	}
	if isUniqueViolation(err) {
		return proxyservice.Proxy{}, errors.New("proxy name already exists")
	}
	return item, err
}

func (s *Store) DeleteProxy(ctx context.Context, workspaceID, proxyID string, expectedVersion int64, now time.Time) error {
	var assigned bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM proxy_assignments WHERE workspace_id=$1::uuid AND proxy_id=$2::uuid AND deleted_at IS NULL)`, workspaceID, proxyID).Scan(&assigned); err != nil {
		return err
	}
	if assigned {
		return proxyservice.ErrInUse
	}
	tag, err := s.pool.Exec(ctx, `UPDATE proxies SET status='deleted',deleted_at=$4,updated_at=$4,version=version+1 WHERE workspace_id=$1::uuid AND id=$2::uuid AND version=$3 AND deleted_at IS NULL`, workspaceID, proxyID, expectedVersion, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	if _, findErr := s.FindProxy(ctx, workspaceID, proxyID); findErr == nil {
		return proxyservice.ErrVersionConflict
	}
	return proxyservice.ErrNotFound
}

func (s *Store) CreateProxyAssignment(ctx context.Context, assignment proxyservice.Assignment) (proxyservice.Assignment, error) {
	var profileID, accountID, instanceID string
	switch assignment.TargetType {
	case "profile":
		profileID = assignment.TargetID
	case "account":
		accountID = assignment.TargetID
	case "browser_instance":
		instanceID = assignment.TargetID
	default:
		return proxyservice.Assignment{}, proxyservice.ErrAssignmentConflict
	}
	item, err := scanAssignment(s.pool.QueryRow(ctx, `INSERT INTO proxy_assignments
	(id,workspace_id,proxy_id,profile_id,account_id,browser_instance_id,target_type,version,created_by,created_at,updated_at)
	VALUES ($1::uuid,$2::uuid,$3::uuid,NULLIF($4,'')::uuid,NULLIF($5,'')::uuid,NULLIF($6,'')::uuid,$7,$8,NULLIF($9,'')::uuid,$10,$11)
	RETURNING id::text,workspace_id::text,proxy_id::text,
	COALESCE(profile_id,account_id,browser_instance_id)::text,target_type,version,
	COALESCE(created_by::text,''),created_at,updated_at,deleted_at`,
		assignment.ID, assignment.WorkspaceID, assignment.ProxyID, profileID, accountID, instanceID,
		assignment.TargetType, assignment.Version, assignment.CreatedBy, assignment.CreatedAt, assignment.UpdatedAt))
	if isUniqueViolation(err) {
		current, findErr := s.FindProxyAssignment(ctx, assignment.WorkspaceID, assignment.TargetID, assignment.TargetType)
		if findErr == nil && current.ProxyID == assignment.ProxyID {
			return current, nil
		}
		return proxyservice.Assignment{}, proxyservice.ErrAssignmentConflict
	}
	if isForeignKeyViolation(err) {
		return proxyservice.Assignment{}, proxyservice.ErrNotFound
	}
	return item, err
}

func (s *Store) FindProxyAssignment(ctx context.Context, workspaceID, targetID, targetType string) (proxyservice.Assignment, error) {
	return scanAssignment(s.pool.QueryRow(ctx, `SELECT id::text,workspace_id::text,proxy_id::text,
		COALESCE(profile_id,account_id,browser_instance_id)::text,target_type,version,
		COALESCE(created_by::text,''),created_at,updated_at,deleted_at
		FROM proxy_assignments
		WHERE workspace_id=$1::uuid AND COALESCE(profile_id,account_id,browser_instance_id)=$2::uuid
		AND target_type=$3 AND deleted_at IS NULL`, workspaceID, targetID, targetType))
}

func (s *Store) DeleteProxyAssignment(ctx context.Context, workspaceID, targetID, targetType string, expectedVersion int64, now time.Time) error {
	tag, err := s.pool.Exec(ctx, `UPDATE proxy_assignments SET deleted_at=$5,updated_at=$5,version=version+1
		WHERE workspace_id=$1::uuid AND COALESCE(profile_id,account_id,browser_instance_id)=$2::uuid
		AND target_type=$3 AND version=$4 AND deleted_at IS NULL`, workspaceID, targetID, targetType, expectedVersion, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	if _, findErr := s.FindProxyAssignment(ctx, workspaceID, targetID, targetType); findErr == nil {
		return proxyservice.ErrVersionConflict
	}
	return proxyservice.ErrNotFound
}

func (s *Store) CreateProxyHealthCheck(ctx context.Context, check proxyservice.HealthCheck) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	_, err = tx.Exec(ctx, `INSERT INTO proxy_health_checks (id,workspace_id,proxy_id,request_id,connector_type,kernel,status,created_by,created_at) VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6,$7,NULLIF($8,'')::uuid,$9)`, check.ID, check.WorkspaceID, check.ProxyID, check.RequestID, check.ConnectorType, check.Kernel, check.Status, check.CreatedBy, check.CreatedAt)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO tasks
		(id,workspace_id,task_type,requested_by,idempotency_key,status,payload,retry_limit,available_at,created_at,updated_at)
		VALUES ($1::uuid,$2::uuid,'proxy.health_check',NULLIF($3,'')::uuid,$4,'queued',
		jsonb_build_object('checkId',$5::text,'proxyId',$6::text,'connectorType',$7::text,'kernel',$8::text),3,$9,$9,$9)`,
		check.RequestID, check.WorkspaceID, check.CreatedBy, "proxy-health:"+check.ID,
		check.ID, check.ProxyID, check.ConnectorType, check.Kernel, check.CreatedAt)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) FindProxyHealthCheck(ctx context.Context, workspaceID, checkID string) (proxyservice.HealthCheck, error) {
	return scanHealthCheck(s.pool.QueryRow(ctx, `SELECT id::text,workspace_id::text,proxy_id::text,request_id::text,connector_type,kernel,status,COALESCE(ip::text,''),COALESCE(latency_ms,0),COALESCE(error_code,''),COALESCE(error_message,''),COALESCE(created_by::text,''),created_at,completed_at FROM proxy_health_checks WHERE workspace_id=$1::uuid AND id=$2::uuid`, workspaceID, checkID))
}

func (s *Store) ListProxyHealthChecks(ctx context.Context, workspaceID, proxyID string, limit int) ([]proxyservice.HealthCheck, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text,workspace_id::text,proxy_id::text,request_id::text,connector_type,kernel,status,COALESCE(ip::text,''),COALESCE(latency_ms,0),COALESCE(error_code,''),COALESCE(error_message,''),COALESCE(created_by::text,''),created_at,completed_at FROM proxy_health_checks WHERE workspace_id=$1::uuid AND ($2='' OR proxy_id=NULLIF($2,'')::uuid) ORDER BY created_at DESC LIMIT $3`, workspaceID, proxyID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]proxyservice.HealthCheck, 0)
	for rows.Next() {
		item, scanErr := scanHealthCheck(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) CompleteProxyHealthCheck(ctx context.Context, workspaceID, checkID string, result proxyservice.HealthResult, now time.Time) (proxyservice.HealthCheck, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return proxyservice.HealthCheck{}, err
	}
	defer rollback(ctx, tx)
	check, err := scanHealthCheck(tx.QueryRow(ctx, `UPDATE proxy_health_checks SET
		status=$3,ip=NULLIF($4,'')::inet,latency_ms=$5,error_code=$6,error_message=$7,completed_at=$8
		WHERE workspace_id=$1::uuid AND id=$2::uuid AND completed_at IS NULL
		RETURNING id::text,workspace_id::text,proxy_id::text,request_id::text,connector_type,kernel,status,
		COALESCE(ip::text,''),COALESCE(latency_ms,0),COALESCE(error_code,''),COALESCE(error_message,''),
		COALESCE(created_by::text,''),created_at,completed_at`,
		workspaceID, checkID, result.Status, result.IP, result.LatencyMS, result.ErrorCode, result.ErrorMessage, now))
	if errors.Is(err, proxyservice.ErrNotFound) {
		existing, findErr := scanHealthCheck(tx.QueryRow(ctx, `SELECT id::text,workspace_id::text,proxy_id::text,
			request_id::text,connector_type,kernel,status,COALESCE(ip::text,''),COALESCE(latency_ms,0),
			COALESCE(error_code,''),COALESCE(error_message,''),COALESCE(created_by::text,''),created_at,completed_at
			FROM proxy_health_checks WHERE workspace_id=$1::uuid AND id=$2::uuid`, workspaceID, checkID))
		if findErr != nil {
			return proxyservice.HealthCheck{}, findErr
		}
		return existing, nil
	}
	if err != nil {
		return proxyservice.HealthCheck{}, err
	}
	succeeded := result.Status == "succeeded"
	// Administrative failures are not network-quality measurements and must
	// not overwrite the state of a proxy whose route has since changed.
	if !succeeded && result.ErrorCode != "proxy_unreachable" {
		if err := tx.Commit(ctx); err != nil {
			return proxyservice.HealthCheck{}, err
		}
		return check, nil
	}
	_, err = tx.Exec(ctx, `INSERT INTO proxy_health_samples
		(id,workspace_id,proxy_id,connector_type,success,latency_ms,public_ip,error_code,checked_at)
		VALUES (gen_random_uuid(),$1::uuid,$2::uuid,$3,$4,$5,NULLIF($6,'')::inet,$7,$8)`,
		workspaceID, check.ProxyID, check.ConnectorType, succeeded, result.LatencyMS, result.IP, result.ErrorCode, now)
	if err != nil {
		return proxyservice.HealthCheck{}, err
	}
	proxyStatus := "unhealthy"
	if succeeded {
		proxyStatus = "active"
	}
	_, err = tx.Exec(ctx, `UPDATE proxies SET status=$3,updated_at=$4,version=version+1
		WHERE workspace_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL AND status IN ('active','unhealthy')`,
		workspaceID, check.ProxyID, proxyStatus, now)
	if err != nil {
		return proxyservice.HealthCheck{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return proxyservice.HealthCheck{}, err
	}
	return check, nil
}

func scanProxy(row scanner) (proxyservice.Proxy, error) {
	var p proxyservice.Proxy
	if err := row.Scan(&p.ID, &p.WorkspaceID, &p.Name, &p.Protocol, &p.Host, &p.Port, &p.Username, &p.HasCredentials, &p.SecretRef, &p.ConnectorType, &p.Kernel, &p.Status, &p.Version, &p.CreatedBy, &p.CreatedAt, &p.UpdatedAt, &p.DeletedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return proxyservice.Proxy{}, proxyservice.ErrNotFound
		}
		return proxyservice.Proxy{}, err
	}
	return p, nil
}

func scanAssignment(row scanner) (proxyservice.Assignment, error) {
	var a proxyservice.Assignment
	if err := row.Scan(&a.ID, &a.WorkspaceID, &a.ProxyID, &a.TargetID, &a.TargetType, &a.Version, &a.CreatedBy, &a.CreatedAt, &a.UpdatedAt, &a.DeletedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return proxyservice.Assignment{}, proxyservice.ErrNotFound
		}
		return proxyservice.Assignment{}, err
	}
	return a, nil
}

func scanHealthCheck(row scanner) (proxyservice.HealthCheck, error) {
	var h proxyservice.HealthCheck
	if err := row.Scan(&h.ID, &h.WorkspaceID, &h.ProxyID, &h.RequestID, &h.ConnectorType, &h.Kernel, &h.Status, &h.IP, &h.LatencyMS, &h.ErrorCode, &h.ErrorMessage, &h.CreatedBy, &h.CreatedAt, &h.CompletedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return proxyservice.HealthCheck{}, proxyservice.ErrNotFound
		}
		return proxyservice.HealthCheck{}, err
	}
	return h, nil
}
