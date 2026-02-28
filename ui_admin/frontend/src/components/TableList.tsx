import { useEffect, useState, useMemo } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { api } from '../api/client';

export default function TableList() {
  const [tables, setTables] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [searchQuery, setSearchQuery] = useState('');
  const navigate = useNavigate();

  useEffect(() => {
    loadTables();
  }, []);

  const loadTables = async () => {
    setLoading(true);
    setError(null);
    try {
      const names = await api.listTables();
      setTables(names);
    } catch (err: any) {
      setError(err.message);
    } finally {
      setLoading(false);
    }
  };

  const handleDrop = async (name: string) => {
    if (!confirm(`Drop table "${name}"? This action cannot be undone.`)) return;

    try {
      await api.dropTable(name);
      await loadTables();
    } catch (err: any) {
      alert(`Failed to drop table: ${err.message}`);
    }
  };

  // Filter tables based on search query
  const filteredTables = useMemo(() => {
    if (!searchQuery.trim()) {
      return tables;
    }
    const query = searchQuery.toLowerCase();
    return tables.filter(name => name.toLowerCase().includes(query));
  }, [tables, searchQuery]);

  if (loading) return <div className="loading">Loading...</div>;
  if (error) return <div className="error">Error: {error}</div>;

  return (
    <div className="table-list">
      <h2>Tables</h2>

      <div className="table-controls">
        <div className="search-controls">
          <input
            type="text"
            placeholder="Search tables..."
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            className="search-input"
          />
          {searchQuery && (
            <button className="secondary" onClick={() => setSearchQuery('')}>
              Clear
            </button>
          )}
        </div>
        <div className="action-controls">
          <Link to="/tables/new">
            <button>Create New Table</button>
          </Link>
        </div>
      </div>

      <div className="results-info">
        Showing {filteredTables.length} of {tables.length} tables
        {searchQuery && ` matching "${searchQuery}"`}
      </div>

      {filteredTables.length > 0 ? (
        <table>
          <thead>
            <tr>
              <th>Name</th>
              <th>Actions</th>
            </tr>
          </thead>
          <tbody>
            {filteredTables.map(name => (
              <tr key={name}>
                <td>{name}</td>
                <td>
                  <button onClick={() => navigate(`/tables/${name}`)}>View</button>
                  <button className="danger" onClick={() => handleDrop(name)}>Drop</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : (
        <p>
          {searchQuery
            ? `No tables match your search "${searchQuery}".`
            : 'No tables found. Create your first table above.'}
        </p>
      )}
    </div>
  );
}
