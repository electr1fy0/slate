# Classroom API

Classrooms are teacher-owned spaces backed by the existing single-table store. The API accepts both the historical root resource prefix and the conventional `/api` prefix:

| Method | Path | Authorization | Purpose |
|---|---|---|---|
| `POST` | `/api/classrooms` | teacher | Create a class from `{ "name": string, "description": string }`. The response includes the teacher's `join_code`. |
| `GET` | `/api/classrooms` | teacher/student | List classes owned by the teacher or joined by the student. |
| `GET` | `/api/classrooms/{id}` | owner/member | Read a class. Join codes are redacted from student responses. |
| `PATCH` / `PUT` | `/api/classrooms/{id}` | owner teacher | Update `name` and/or `description`. |
| `DELETE` | `/api/classrooms/{id}` | owner teacher | Delete a class and its roster. |
| `POST` | `/api/classrooms/join` | student | Join with `{ "join_code": string }` (the `code` field is also accepted). |
| `GET` | `/api/classrooms/{id}/members` | owner teacher | List the student roster. `/roster` is an alias. |
| `DELETE` | `/api/classrooms/{id}/members/{user_id}` | owner teacher | Remove one student. |

The same paths without `/api` are supported for existing clients. Mutating routes respect the existing active/standby role gate.

The persistence layout uses `CLASSROOM#<id>/META`, `CLASSROOM#<id>/MEMBER#<user_id>`, plus owner, member, and join-code lookup records. Join and roster writes are conditional/transactional in DynamoDB; the memory store mirrors the same ownership, duplicate-join, and deletion semantics for local development and tests.

Existing seeded exams remain public to authenticated users, and board rooms remain compatible with their current room-token contract. Classroom membership is enforced for classroom resources and is ready to be threaded into board/quiz ownership once those legacy resource handlers are migrated; this slice intentionally does not change their routes or demo behavior.
