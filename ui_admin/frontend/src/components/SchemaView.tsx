import { useState } from 'react';
import { TableSchema, api } from '../api/client';

interface Props {
  schema: TableSchema;
  onRefresh?: () => void;
}

export default function SchemaView({ schema, onRefresh }: Props) {
  const [busy, setBusy] = useState<string | null>(null); // column name being acted on
  const [error, setError] = useState<string | null>(null);

  const handleCreateIndex = async (column: string, unique: boolean) => {
    setBusy(column);
    setError(null);
    try {
      await api.createIndex(schema.name, column, unique);
      onRefresh?.();
    } catch (e: any) {
      setError(e.message);
    } finally {
      setBusy(null);
    }
  };

  const handleDropIndex = async (column: string) => {
    if (!confirm(`Drop index on "${column}"?`)) return;
    setBusy(column);
    setError(null);
    try {
      await api.dropIndex(schema.name, column);
      onRefresh?.();
    } catch (e: any) {
      setError(e.message);
    } finally {
      setBusy(null);
    }
  };

  return (
    <div className="schema-view">
      <h3>Schema</h3>
      {error && <div className="error" style={{ marginBottom: 8 }}>{error}</div>}
      <table className="schema-table">
        <thead>
          <tr>
            <th>Column</th>
            <th>Type</th>
            <th>Nullable</th>
            <th>Index</th>
            <th>Actions</th>
          </tr>
        </thead>
        <tbody>
          {schema.columns.map(col => (
            <tr key={col.name}>
              <td>{col.name}</td>
              <td>{col.type}</td>
              <td>{col.nullable ? 'Yes' : 'No'}</td>
              <td>
                {col.indexed
                  ? (col.index_unique
                      ? <span className="badge badge-unique">UNIQUE</span>
                      : <span className="badge badge-index">INDEX</span>)
                  : <span className="badge badge-none">—</span>}
              </td>
              <td className="index-actions">
                {busy === col.name ? (
                  <span className="muted">...</span>
                ) : col.indexed ? (
                  <button
                    className="danger small"
                    onClick={() => handleDropIndex(col.name)}
                  >
                    Drop Index
                  </button>
                ) : (
                  <>
                    <button
                      className="secondary small"
                      onClick={() => handleCreateIndex(col.name, false)}
                    >
                      Index
                    </button>
                    <button
                      className="secondary small"
                      onClick={() => handleCreateIndex(col.name, true)}
                    >
                      Unique
                    </button>
                  </>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
