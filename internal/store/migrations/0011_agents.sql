CREATE TABLE agents (
    name text PRIMARY KEY CHECK (name IN ('eika', 'worker', 'researcher')),
    class text CHECK (class IN ('main', 'fast', 'deep')),
    prompt text CHECK (octet_length(prompt) <= 65536)
);
