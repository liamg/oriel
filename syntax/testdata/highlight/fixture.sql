-- tiers by revenue
SELECT t.name, SUM(o.amount) AS revenue
FROM tiers AS t
JOIN orders AS o ON o.tier_id = t.id
WHERE o.created_at >= '2026-01-01'
GROUP BY t.name
ORDER BY revenue DESC
LIMIT 10;
