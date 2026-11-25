-- Preserve the model that runs without an explicit choice for installations
-- created before the main class was assigned. Keep every existing assignment.
WITH chosen AS (
    SELECT m.name
    FROM models m
    LEFT JOIN settings d ON d.key = 'default_model'
    WHERE m.kind = 'chat'
    ORDER BY (m.name = d.value #>> '{}') DESC NULLS LAST, m.created_at, m.id
    LIMIT 1
)
INSERT INTO settings (key, value)
SELECT 'model_classes', jsonb_build_object('main', name) FROM chosen
ON CONFLICT (key) DO UPDATE
SET value = (CASE WHEN jsonb_typeof(settings.value) = 'object' THEN settings.value ELSE '{}'::jsonb END) || EXCLUDED.value
WHERE jsonb_typeof(settings.value) <> 'object'
   OR coalesce(settings.value ->> 'main', '') = '';
