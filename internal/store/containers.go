package store

import (
	"context"
	"encoding/json"
)

// --- containers ---------------------------------------------------------------
//
// Ports/Env/Volumes are stored as JSON text columns and decoded straight
// into the Container struct's typed fields here, so every other package
// works with real []PortMap/map[string]string/[]VolumeMount values and never
// touches the JSON encoding directly.

const containerCols = `id, user_id, domain_id, name, image, ports, web_port, env, volumes,
	restart_policy, memory_limit_mb, cpu_limit, created_at`

func scanContainer(row interface{ Scan(...any) error }) (*Container, error) {
	var c Container
	var ports, env, volumes string
	var created interface{}
	if err := row.Scan(&c.ID, &c.UserID, &c.DomainID, &c.Name, &c.Image, &ports, &c.WebPort,
		&env, &volumes, &c.RestartPolicy, &c.MemoryLimitMB, &c.CPULimit, &created); err != nil {
		return nil, wrapErr(err)
	}
	c.CreatedAt = parseTime(created)
	c.Ports = []PortMap{}
	_ = json.Unmarshal([]byte(ports), &c.Ports)
	c.Env = map[string]string{}
	_ = json.Unmarshal([]byte(env), &c.Env)
	c.Volumes = []VolumeMount{}
	_ = json.Unmarshal([]byte(volumes), &c.Volumes)
	return &c, nil
}

func (s *Store) CreateContainer(ctx context.Context, c *Container) error {
	ports, err := json.Marshal(c.Ports)
	if err != nil {
		return err
	}
	env, err := json.Marshal(c.Env)
	if err != nil {
		return err
	}
	volumes, err := json.Marshal(c.Volumes)
	if err != nil {
		return err
	}
	ts := now()
	res, err := s.db.ExecContext(ctx, `INSERT INTO containers (user_id, domain_id, name, image,
		ports, web_port, env, volumes, restart_policy, memory_limit_mb, cpu_limit, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.UserID, c.DomainID, c.Name, c.Image, string(ports), c.WebPort, string(env),
		string(volumes), c.RestartPolicy, c.MemoryLimitMB, c.CPULimit, ts)
	if err != nil {
		return wrapErr(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	c.ID = id
	c.CreatedAt = parseTime(ts)
	return nil
}

func (s *Store) GetContainer(ctx context.Context, id int64) (*Container, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+containerCols+" FROM containers WHERE id = ?", id)
	return scanContainer(row)
}

// ListContainers returns containers; when userID > 0, only that user's rows.
func (s *Store) ListContainers(ctx context.Context, userID int64) ([]*Container, error) {
	q := "SELECT " + containerCols + " FROM containers"
	args := []interface{}{}
	if userID > 0 {
		q += " WHERE user_id = ?"
		args = append(args, userID)
	}
	q += " ORDER BY id"
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Container{}
	for rows.Next() {
		c, err := scanContainer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CountContainers reports how many containers a user owns (package quota).
func (s *Store) CountContainers(ctx context.Context, userID int64) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM containers WHERE user_id = ?", userID).Scan(&n)
	return n, err
}

// SetContainerAttachment updates only the domain/web-port attachment —
// called by svc.Docker on attach/detach without touching the rest of the row.
func (s *Store) SetContainerAttachment(ctx context.Context, id, domainID int64, webPort int) error {
	_, err := s.db.ExecContext(ctx, "UPDATE containers SET domain_id=?, web_port=? WHERE id=?", domainID, webPort, id)
	return wrapErr(err)
}

func (s *Store) DeleteContainer(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM containers WHERE id = ?", id)
	return err
}
