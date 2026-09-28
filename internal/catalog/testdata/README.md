# Recorded API fixtures

Captured on 2026-09-28 from https://groupietrackers.herokuapp.com/api.

The four JSON files are unmodified upstream responses used by local HTTP test servers. They are not embedded into the production executable and are never a fallback for visitors. Source names and spellings are intentionally preserved.

| File | Source | SHA-256 |
| --- | --- | --- |
| `artists.json` | `/api/artists` | `5de19d94c7e6680601553c4965c67a7b0e5f36d6096ad2a202fe4f0721747108` |
| `locations.json` | `/api/locations` | `055a09cbaff65994b38c41e0d559a48bc66f11b7cce3e4380942e5df2dad2d38` |
| `dates.json` | `/api/dates` | `45c2a452e83bce704dcd6836a9c831eb902507cbc283b694486eb8cb97acbd1a` |
| `relation.json` | `/api/relation` | `2476f36799a21c4b09c825e582274bd6d5b4cd839a928acc50943cd8d2d653db` |

Reference snapshot: 52 artists, 441 date entries. Travis Scott: 10 locations, 12 dates, 6 countries. The current source says `sao_paulo-brazil`; the subject uses `sao_paulo-brasil`. Tests normalize this country alias without modifying displayed source data.
