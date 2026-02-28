import { useState } from 'react';
import { TableSchema } from '../api/client';

interface Props {
  schema: TableSchema;
  initialValues?: Record<string, any>;
  onSave: (values: Record<string, any>) => Promise<void>;
  onCancel: () => void;
}

export default function RowEditor({ schema, initialValues, onSave, onCancel }: Props) {
  const [values, setValues] = useState<Record<string, any>>(initialValues || {});
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const updateValue = (colName: string, value: any) => {
    setValues({ ...values, [colName]: value });
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    setSubmitting(true);

    try {
      await onSave(values);
    } catch (err: any) {
      setError(err.message);
      setSubmitting(false);
    }
  };

  const getInputType = (colType: string) => {
    if (colType === 'INT' || colType === 'BIGINT') return 'number';
    if (colType === 'FLOAT' || colType === 'DOUBLE') return 'number';
    if (colType === 'BOOLEAN') return 'checkbox';
    if (colType === 'DATETIME') return 'datetime-local';
    return 'text';
  };

  return (
    <div className="modal">
      <div className="modal-content">
        <h3>{initialValues ? 'Edit Row' : 'Insert Row'}</h3>

        {error && <div className="error">{error}</div>}

        <form onSubmit={handleSubmit}>
          {schema.columns.map(col => {
            const inputType = getInputType(col.type);
            const currentValue = values[col.name];

            if (col.type === 'BOOLEAN') {
              return (
                <label key={col.name}>
                  <input
                    type="checkbox"
                    checked={currentValue === true || currentValue === 1}
                    onChange={e => updateValue(col.name, e.target.checked)}
                  />
                  {col.name} ({col.type})
                </label>
              );
            }

            return (
              <label key={col.name}>
                {col.name} ({col.type}){col.nullable ? '' : ' *'}:
                <input
                  type={inputType}
                  value={currentValue ?? ''}
                  onChange={e => {
                    const val = e.target.value;
                    if (inputType === 'number') {
                      updateValue(col.name, val === '' ? null : parseFloat(val));
                    } else {
                      updateValue(col.name, val === '' ? null : val);
                    }
                  }}
                  required={!col.nullable}
                  disabled={submitting}
                  step={col.type === 'FLOAT' || col.type === 'DOUBLE' ? 'any' : undefined}
                />
              </label>
            );
          })}

          <div className="actions">
            <button type="submit" disabled={submitting}>
              {submitting ? 'Saving...' : 'Save'}
            </button>
            <button type="button" className="secondary" onClick={onCancel} disabled={submitting}>
              Cancel
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
