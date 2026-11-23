-- Runs of the worker (SC-API-155, README §5.4): each task writes a row when
-- it starts and ends it when it stops, failure included, for the admin
-- dashboard (last run and last failure of each task, §2.12). Only the 500
-- most recent rows of each task are kept, plus its last failure however old,
-- trimmed by the worker at the end of each run. No personal data: the outcome and a stable error code only, never
-- an error message. started_at has no default: the worker sets it from
-- platform/clock.
CREATE TABLE worker_runs (
    id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    task       text        NOT NULL,
    started_at timestamptz NOT NULL,
    -- Both null while the run goes on, or when the process was killed before
    -- it could end the row (a Cloud Run job stopped at its time limit).
    ended_at   timestamptz,
    outcome    text,
    -- Set for a failure only: an apperr code (snake_case), never free text.
    error_code text,
    CONSTRAINT worker_runs_task_check
        CHECK (task IN ('close-weeks', 'finalize-challenges', 'retry-auth0-deletions')),
    CONSTRAINT worker_runs_outcome_check CHECK (outcome IN ('succeeded', 'failed')),
    CONSTRAINT worker_runs_ended_with_outcome_check CHECK ((ended_at IS NULL) = (outcome IS NULL)),
    CONSTRAINT worker_runs_ended_after_started_check CHECK (ended_at >= started_at),
    CONSTRAINT worker_runs_error_code_on_failure_check
        CHECK ((outcome IS NOT DISTINCT FROM 'failed') = (error_code IS NOT NULL)),
    CONSTRAINT worker_runs_error_code_check CHECK (error_code ~ '^[a-z][a-z0-9_]*$' AND char_length(error_code) <= 64)
);

-- The runs of a task, most recent first: the trimming to the last 500, and
-- the last run and last failure of the dashboard.
CREATE INDEX worker_runs_task_started_at_idx ON worker_runs (task, started_at DESC, id DESC);
