UPDATE computers
SET sandbox = jsonb_set(CASE WHEN jsonb_typeof(sandbox) = 'object' THEN sandbox ELSE '{}'::jsonb END, '{egress}', '"public"'::jsonb)
WHERE jsonb_typeof(sandbox) <> 'object' OR sandbox->>'egress' IS NULL OR sandbox->>'egress' IN ('', 'open');

UPDATE settings
SET value = jsonb_set(CASE WHEN jsonb_typeof(value) = 'object' THEN value ELSE '{}'::jsonb END, '{mode}', '"public"'::jsonb)
WHERE key = 'sandbox_egress' AND (jsonb_typeof(value) <> 'object' OR value->>'mode' IS NULL OR value->>'mode' IN ('', 'open'));
