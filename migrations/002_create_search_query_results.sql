CREATE TABLE search_query_results (
    search_query_id BIGINT NOT NULL,
    listing_id BIGINT NOT NULL,

    position INTEGER NOT NULL DEFAULT 0,

    created_at TIMESTAMP NOT NULL DEFAULT NOW(),

    PRIMARY KEY (search_query_id, listing_id),

    CONSTRAINT search_query_results_search_query_id_fkey
        FOREIGN KEY (search_query_id)
        REFERENCES search_queries(id)
        ON DELETE CASCADE,

    CONSTRAINT search_query_results_listing_id_fkey
        FOREIGN KEY (listing_id)
        REFERENCES part_listings(id)
        ON DELETE CASCADE
);

CREATE INDEX idx_search_query_results_search_query_id
    ON search_query_results(search_query_id);

CREATE INDEX idx_search_query_results_listing_id
    ON search_query_results(listing_id);