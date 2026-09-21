CREATE TABLE search_queries (
    id BIGSERIAL PRIMARY KEY,

    source_id INTEGER NOT NULL,

    query TEXT NOT NULL,

    city VARCHAR(150),

    last_searched_at TIMESTAMP NOT NULL,

    created_at TIMESTAMP NOT NULL DEFAULT NOW(),

    CONSTRAINT search_queries_source_id_fkey
        FOREIGN KEY (source_id)
        REFERENCES sources(id)
        ON DELETE CASCADE,

    CONSTRAINT search_queries_unique
        UNIQUE (source_id, query, city)
);