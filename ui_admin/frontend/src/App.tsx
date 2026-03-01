import { useCallback, useEffect, useState } from 'react';
import { BrowserRouter, Routes, Route, Link, useLocation, useNavigate } from 'react-router-dom';
import DatabaseInfo from './components/DatabaseInfo';
import TableList from './components/TableList';
import TableView from './components/TableView';
import TableCreate from './components/TableCreate';
import SqlConsole from './components/SqlConsole';
import { api } from './api/client';

interface NavigationProps {
  currentDB: string;
  databases: string[];
  onSwitch: (name: string) => void;
}

function Navigation({ currentDB, databases, onSwitch }: NavigationProps) {
  const location = useLocation();

  const handleSelect = (e: React.ChangeEvent<HTMLSelectElement>) => {
    const name = e.target.value;
    if (name && name !== currentDB) {
      onSwitch(name);
    }
  };

  return (
    <nav className="navbar">
      <h1>DB Internals Admin</h1>
      {databases.length > 0 && (
        <select
          className="db-selector"
          value={currentDB}
          onChange={handleSelect}
          title="Switch database"
        >
          {databases.map(db => (
            <option key={db} value={db}>{db}</option>
          ))}
        </select>
      )}
      <div className="nav-links">
        <Link to="/" className={location.pathname === '/' ? 'active' : ''}>
          Home
        </Link>
        <Link to="/tables" className={location.pathname.startsWith('/tables') ? 'active' : ''}>
          Tables
        </Link>
        <Link to="/query" className={location.pathname === '/query' ? 'active' : ''}>
          SQL
        </Link>
      </div>
    </nav>
  );
}

// AppInner must live inside BrowserRouter so it can use useNavigate.
function AppInner() {
  const [currentDB, setCurrentDB] = useState('');
  const [databases, setDatabases] = useState<string[]>([]);
  const navigate = useNavigate();

  const loadDBInfo = useCallback(async () => {
    try {
      const [info, dbs] = await Promise.all([api.getInfo(), api.listDatabases()]);
      setCurrentDB(info.current_db);
      setDatabases(dbs);
    } catch {
      // ignore — components will show their own errors
    }
  }, []);

  useEffect(() => {
    loadDBInfo();
  }, [loadDBInfo]);

  const handleSwitch = async (name: string) => {
    try {
      await api.switchDatabase(name);
      setCurrentDB(name);
      await loadDBInfo();
      navigate('/');
    } catch (err) {
      console.error('Failed to switch database:', err);
    }
  };

  return (
    <div className="app">
      <Navigation currentDB={currentDB} databases={databases} onSwitch={handleSwitch} />
      <main className="content">
        <Routes>
          <Route path="/" element={<DatabaseInfo />} />
          <Route path="/tables" element={<TableList />} />
          <Route path="/tables/new" element={<TableCreate />} />
          <Route path="/tables/:name" element={<TableView />} />
          <Route path="/query" element={<SqlConsole onDBSwitch={loadDBInfo} />} />
        </Routes>
      </main>
    </div>
  );
}

function App() {
  return (
    <BrowserRouter>
      <AppInner />
    </BrowserRouter>
  );
}

export default App;
