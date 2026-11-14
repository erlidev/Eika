CREATE TABLE projects (
    id text PRIMARY KEY,
    name text NOT NULL CHECK (length(btrim(name)) > 0 AND octet_length(name) <= 200),
    slug text NOT NULL UNIQUE CHECK (octet_length(slug) <= 80 AND slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    description text NOT NULL DEFAULT '' CHECK (octet_length(description) <= 4096),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'paused', 'done', 'archived')),
    directory text NOT NULL CHECK (
        octet_length(directory) <= 4096 AND directory LIKE '/%'
        AND directory <> '/' AND directory !~ '[[:cntrl:]]'
        AND position(chr(92) in directory) = 0
        AND directory !~ '(^|/)[.]{1,2}(/|$)'
        AND directory NOT LIKE '%//%' AND right(directory, 1) <> '/'
    ),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
