-- Account merges preserve the original owner on immutable facts. This function
-- exposes a registered account together with every anonymous account that was
-- merged into it, so read paths can retain that history without rewriting it.
CREATE OR REPLACE FUNCTION lingow_account_lineage(root_account_id TEXT)
RETURNS TABLE(account_id TEXT)
LANGUAGE sql
STABLE
AS $$
    WITH RECURSIVE lineage AS (
        SELECT id, ARRAY[id] AS visited
        FROM lingow_accounts
        WHERE id = root_account_id

        UNION ALL

        SELECT child.id, parent.visited || child.id
        FROM lingow_accounts AS child
        JOIN lineage AS parent ON child.merged_into = parent.id
        WHERE NOT child.id = ANY(parent.visited)
    )
    SELECT id FROM lineage;
$$;
