# v3.2 (upcoming)

Features
- add OIDC compatibility and docker compose example
- add COORDINATE field type to store location
- auto-extract location from uploads in the frontend if available

Improvements:
- cleaning up of CSS styles

# v3.1

Features:
- add Postgres and S3 compatibility

Improvements:
- improved entry upload, enabling multi-file uploads from mobile devices
- improved touch screen support for selecting files
- improved preview querying and caching
- default to 0 days max age for new databases instead of 365 days
- add Docusaurus docs

Bug fixes:
- fix storage not being cleaned when deleting full database
- fix missing timestamp in JSON inserting timestamp far in the past
- fix time display for timestamp <1970
- fix export not including media fields
- multiple minor bug fixes, mainly issues found through LLM analysis

# v3.0

Features:
- allow retrieving entries by `created_at` or `updated_at` instead of `timestamp`
- add API-Key management with service accounts
- add `can_admin` role on database level, to allow standard users to manage database settings

Improvements:
- remove audit log button if audit logs not available/not stored in DB
- frontend complete rework: 
    * fullscreen entry dashboard, with improved tiling
    * light mode
    * home dashbord showing databases, instead of in the sidebar
    * new page and modals for API key management
    * new page for user settings
    * new icons and logo
- improve middleware performance

Bug fixes:
- correctly index custom fields
- do not show content above header in profile page anymore

Breaking Changes:
- user id are now ULID instead of simple integers
- user endpoints to use path parameter instead of query parameter
- preview generation now clipping aspect ratios for stretched images 


# v2.1

Bug fixes:
- fix lookup for expired refresh token

Features:
- add entry queuing and processing limits, to avoid server resource starvation. New config arguments: `server.processing.n_ffmpeg_async` and `server.processing.n_ffmpeg_total`. `n_max_queued` parameter on database level.
- allow adding, renaming and deleting custom fields after database creation.
- add option to not index custom field columns via `is_indexed=false`.
- automatically extract timestamp from certain file format in the frontend
- allow drag and drop of multiple files at once in the frontend
- modernize entry grid view with tiling
- adjust preview generation to clip aspect ration, for frontend alignment

# v2.0.1

Bug Fixes:
- fix display of wrong timestamp in entry list view 

Improvements:
- add font scaling for small screens
- add https as possible swagger schema

# v2.0

Features:
- add video file support
- add fullscreen playback to frontend
- user roles now on database level

Breaking Changes:
- all timestamps are now stored with millisecond precision
- entry and database endpoint paths changed to use path variables
- files are now stored in folders by `id` rather than by `date`
- database schema changed significantly
- running the server now requires the `serve` command
- configuration file has renamed configs
- renamed environment variable prefix from `FDB_` to `MEDIAHUB_`

Other:
- publish multiarch docker build (ARM64, X86)
- add Windows ARM64 binaries to release

# v1.2

Features:
- make the "large file" threshold configurable.
- implement database schema versions to enable migrations in the future.
- implement a "recovery" command, for database consistency checks after, e.g., a server crash
- implement auditor logging, which has to be enabled explicitly
- implement bulk delete and bulk download functionalities

Bug fixes:
- fix swagger documentation not being up to date
- fix filename and status fields not being whitelisted in the search endpoint

# v1.1

- JWT implemented in the backend and frontend. Basic auth is still available for endpoints, but is to be avoided if possible.
- Drag and drop functionality added to the frontend. This functionality works in the list, grid and uploda views, and checks the mime type before upload as well.
- Reworked entry detail view, including Delete and Edit button, organized properties
- implement content negotiation to enable base64 responses, e.g., for Grafana display of images
- better preview handling and clearer status return flag for entry upload
