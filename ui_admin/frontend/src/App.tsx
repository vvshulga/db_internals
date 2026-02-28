import { BrowserRouter, Routes, Route, Link, useLocation } from 'react-router-dom';
import DatabaseInfo from './components/DatabaseInfo';
import TableList from './components/TableList';
import TableView from './components/TableView';
import TableCreate from './components/TableCreate';

function Navigation() {
  const location = useLocation();

  return (
    <nav className="navbar">
      <h1>DB Internals Admin</h1>
      <div className="nav-links">
        <Link to="/" className={location.pathname === '/' ? 'active' : ''}>
          Home
        </Link>
        <Link to="/tables" className={location.pathname.startsWith('/tables') ? 'active' : ''}>
          Tables
        </Link>
      </div>
    </nav>
  );
}

function App() {
  return (
    <BrowserRouter>
      <div className="app">
        <Navigation />
        <main className="content">
          <Routes>
            <Route path="/" element={<DatabaseInfo />} />
            <Route path="/tables" element={<TableList />} />
            <Route path="/tables/new" element={<TableCreate />} />
            <Route path="/tables/:name" element={<TableView />} />
          </Routes>
        </main>
      </div>
    </BrowserRouter>
  );
}

export default App;
