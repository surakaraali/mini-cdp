-- A separate database for automated tests, so they never touch the data used with `make run`.
CREATE DATABASE cdp_test;

-- A database for performance measurements with 1M generated events (make seed, make bench).
CREATE DATABASE cdp_bench;
