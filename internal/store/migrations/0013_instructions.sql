CREATE TABLE instructions (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    about_me text NOT NULL DEFAULT '' CHECK (octet_length(about_me) <= 65536),
    rules text NOT NULL DEFAULT '' CHECK (octet_length(rules) <= 65536)
);
INSERT INTO instructions DEFAULT VALUES;

CREATE TABLE project_instructions (
    project_id text PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
    instructions text NOT NULL CHECK (octet_length(instructions) <= 65536)
);
