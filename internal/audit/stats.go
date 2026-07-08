package audit

import "context"

// Stats is the aggregate summary the read-only UI renders as stat tiles plus a
// per-server breakdown. Percentiles are overall (across all servers); the
// per-server table carries counts and mean latency, which SQLite computes
// cheaply without a percentile function.
type Stats struct {
	TotalCalls int64
	Errors     int64
	ErrorRate  float64
	P50ms      int64
	P95ms      int64
	PerServer  []ServerStat
}

// ServerStat is one row of the per-server breakdown.
type ServerStat struct {
	Server string
	Calls  int64
	Errors int64
	AvgMS  int64
}

// Stats computes the overall counts and latency percentiles plus a per-server
// breakdown. p50/p95 use SQLite's ORDER BY + OFFSET rather than a percentile
// function (which SQLite lacks): the offset is floor(q*(n-1)) over the non-null
// latencies, giving the nearest-rank quantile in a single query with no rows
// pulled into Go.
func (s *Store) Stats(ctx context.Context) (Stats, error) {
	var st Stats
	if err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*),
       COALESCE(SUM(CASE WHEN error_code IS NOT NULL THEN 1 ELSE 0 END), 0)
FROM audit_logs`).Scan(&st.TotalCalls, &st.Errors); err != nil {
		return Stats{}, err
	}
	if st.TotalCalls > 0 {
		st.ErrorRate = float64(st.Errors) / float64(st.TotalCalls)
	}
	p50, err := s.quantile(ctx, 0.50)
	if err != nil {
		return Stats{}, err
	}
	p95, err := s.quantile(ctx, 0.95)
	if err != nil {
		return Stats{}, err
	}
	st.P50ms, st.P95ms = p50, p95

	rows, err := s.db.QueryContext(ctx, `
SELECT COALESCE(server_name, ''),
       COUNT(*),
       COALESCE(SUM(CASE WHEN error_code IS NOT NULL THEN 1 ELSE 0 END), 0),
       COALESCE(CAST(AVG(latency_ms) AS INTEGER), 0)
FROM audit_logs
GROUP BY server_name
ORDER BY COUNT(*) DESC, server_name`)
	if err != nil {
		return Stats{}, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var ss ServerStat
		if err := rows.Scan(&ss.Server, &ss.Calls, &ss.Errors, &ss.AvgMS); err != nil {
			return Stats{}, err
		}
		st.PerServer = append(st.PerServer, ss)
	}
	if err := rows.Err(); err != nil {
		return Stats{}, err
	}
	return st, nil
}

// quantile returns the nearest-rank q-quantile of latency_ms over rows with a
// non-null latency, or 0 when there are none.
func (s *Store) quantile(ctx context.Context, q float64) (int64, error) {
	var v int64
	err := s.db.QueryRowContext(ctx, `
SELECT COALESCE((
  SELECT latency_ms FROM audit_logs
  WHERE latency_ms IS NOT NULL
  ORDER BY latency_ms
  LIMIT 1 OFFSET (
    SELECT CAST(? * (COUNT(*) - 1) AS INTEGER)
    FROM audit_logs WHERE latency_ms IS NOT NULL
  )
), 0)`, q).Scan(&v)
	return v, err
}
