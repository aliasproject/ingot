# Database Naming Test

This document demonstrates the new automatic database naming functionality.

## Changes Made

### 1. Removed Manual Name Input

- Previously: Users were prompted to enter a database name
- Now: Database name is automatically set to the database type ("mysql" or "postgresql")

### 2. Updated Volume Naming

- Previously: Volumes were named `devbox_db_{name}`
- Now: Volumes are named `devbox_{type}-{version}` (e.g., `devbox_mysql-8.0`)

## Testing the Changes

### Test 1: Add MySQL Database

```bash
./devbox database add
# Select MySQL
# Select version (e.g., 8.0)
# Database will be automatically named "mysql"
# Volume will be named "devbox_mysql-8.0"
```

### Test 2: Add PostgreSQL Database

```bash
./devbox database add
# Select PostgreSQL
# Select version (e.g., 16)
# Database will be automatically named "postgresql"
# Volume will be named "devbox_postgresql-16"
```

### Test 3: List Databases

```bash
./devbox database list
# Should show databases named "mysql" and/or "postgresql"
```

### Test 4: Prevent Duplicates

```bash
./devbox database add
# Try to add MySQL again
# Should show error: "database type 'mysql' is already configured"
```

## Expected Docker Compose Output

With the changes, the generated compose file will contain:

```yaml
# For MySQL 8.0
  mysql:
    container_name: devbox_mysql
    image: mysql:8.0
    volumes:
      - mysql-8.0:/var/lib/mysql

# For PostgreSQL 16
  postgresql:
    container_name: devbox_postgresql
    image: postgres:16
    volumes:
      - postgresql-16:/var/lib/postgresql/data

volumes:
  mysql-8.0:
    name: devbox_mysql-8.0
  postgresql-16:
    name: devbox_postgresql-16
```

## Installation Process

During `devbox install`, databases will now be named after their type instead of "main":

- Previously: Database saved as "main"
- Now: Database saved as "mysql" or "postgresql"

## Benefits

1. **Consistency**: Database names match their actual type
2. **Clarity**: No confusion about which database is which
3. **Version Tracking**: Volume names include version information
4. **Simplicity**: No need to think of custom names
5. **Standards**: Follows common naming conventions
