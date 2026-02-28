import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { api, InfoResponse } from '../api/client';

export default function DatabaseInfo() {
  const [info, setInfo] = useState<InfoResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api.getInfo()
      .then(setInfo)
      .catch(err => setError(err.message))
      .finally(() => setLoading(false));
  }, []);

  if (loading) return <div className="loading">Loading...</div>;
  if (error) return <div className="error">Error: {error}</div>;
  if (!info) return <div className="error">Failed to load database info</div>;

  return (
    <div className="database-info">
      <h2>Database Overview</h2>
      <div className="info-card">
        <p><strong>Directory:</strong> {info.database_dir}</p>
        <p><strong>Tables:</strong> {info.table_count}</p>
      </div>

      {info.table_names.length > 0 && (
        <>
          <h3>Tables</h3>
          <table>
            <thead>
              <tr>
                <th>Table Name</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {info.table_names.map(name => (
                <tr key={name}>
                  <td>{name}</td>
                  <td>
                    <Link to={`/tables/${name}`}>
                      <button>View</button>
                    </Link>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </>
      )}

      {info.table_names.length === 0 && (
        <p>No tables found. <Link to="/tables/new">Create your first table</Link>.</p>
      )}
    </div>
  );
}
