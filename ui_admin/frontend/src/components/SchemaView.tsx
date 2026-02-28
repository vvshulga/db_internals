import { TableSchema } from '../api/client';

interface Props {
  schema: TableSchema;
}

export default function SchemaView({ schema }: Props) {
  return (
    <div className="schema-view">
      <h3>Schema</h3>
      <table className="schema-table">
        <thead>
          <tr>
            <th>Column</th>
            <th>Type</th>
            <th>Nullable</th>
          </tr>
        </thead>
        <tbody>
          {schema.columns.map(col => (
            <tr key={col.name}>
              <td>{col.name}</td>
              <td>{col.type}</td>
              <td>{col.nullable ? 'Yes' : 'No'}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
