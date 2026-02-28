import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { api, Column } from '../api/client';

export default function TableCreate() {
  const navigate = useNavigate();
  const [tableName, setTableName] = useState('');
  const [columns, setColumns] = useState<Column[]>([
    { name: 'id', type: 'INT', nullable: false },
  ]);
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const addColumn = () => {
    setColumns([...columns, { name: '', type: 'INT', nullable: false }]);
  };

  const updateColumn = (index: number, field: keyof Column, value: any) => {
    const updated = [...columns];
    updated[index] = { ...updated[index], [field]: value };
    setColumns(updated);
  };

  const removeColumn = (index: number) => {
    if (columns.length === 1) {
      alert('Table must have at least one column');
      return;
    }
    setColumns(columns.filter((_, i) => i !== index));
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);

    // Validate
    if (!tableName.trim()) {
      setError('Table name is required');
      return;
    }

    for (let i = 0; i < columns.length; i++) {
      if (!columns[i].name.trim()) {
        setError(`Column ${i + 1} name is required`);
        return;
      }
    }

    setSubmitting(true);
    try {
      await api.createTable(tableName, columns);
      navigate('/tables');
    } catch (err: any) {
      setError(err.message);
      setSubmitting(false);
    }
  };

  return (
    <div className="table-create">
      <h2>Create Table</h2>

      {error && <div className="error">{error}</div>}

      <form onSubmit={handleSubmit}>
        <label>
          Table Name:
          <input
            type="text"
            value={tableName}
            onChange={e => setTableName(e.target.value)}
            required
            disabled={submitting}
          />
        </label>

        <h3>Columns</h3>
        {columns.map((col, i) => (
          <div key={i} className="column-row">
            <input
              type="text"
              placeholder="Column name"
              value={col.name}
              onChange={e => updateColumn(i, 'name', e.target.value)}
              required
              disabled={submitting}
            />
            <select
              value={col.type}
              onChange={e => updateColumn(i, 'type', e.target.value)}
              disabled={submitting}
            >
              <option value="INT">INT</option>
              <option value="BIGINT">BIGINT</option>
              <option value="FLOAT">FLOAT</option>
              <option value="DOUBLE">DOUBLE</option>
              <option value="BOOLEAN">BOOLEAN</option>
              <option value="VARCHAR(255)">VARCHAR(255)</option>
              <option value="TEXT">TEXT</option>
              <option value="DATETIME">DATETIME</option>
            </select>
            <label>
              <input
                type="checkbox"
                checked={col.nullable}
                onChange={e => updateColumn(i, 'nullable', e.target.checked)}
                disabled={submitting}
              />
              Nullable
            </label>
            <button
              type="button"
              className="danger"
              onClick={() => removeColumn(i)}
              disabled={submitting || columns.length === 1}
            >
              Remove
            </button>
          </div>
        ))}

        <button type="button" onClick={addColumn} disabled={submitting}>
          Add Column
        </button>

        <div className="actions">
          <button type="submit" disabled={submitting}>
            {submitting ? 'Creating...' : 'Create Table'}
          </button>
          <button
            type="button"
            className="secondary"
            onClick={() => navigate('/tables')}
            disabled={submitting}
          >
            Cancel
          </button>
        </div>
      </form>
    </div>
  );
}
