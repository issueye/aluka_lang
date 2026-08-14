// 验证 Aluka 纯 Go 引擎对 React JSX / TSX 源码级支持

const React = {
  Fragment: 'React.Fragment',
  createElement: function(type, props, ...children) {
    const flatChildren = children.flat ? children.flat(Infinity) : children;
    if (typeof type === 'function') {
      return type(Object.assign({}, props, { children: flatChildren }));
    }
    return {
      type,
      props: props || {},
      children: flatChildren
    };
  }
};

// 1. 自定义 React 函数组件
function Header({ title, badge }) {
  return (
    <header className="header-box">
      <h1>{title}</h1>
      <span className="badge">{badge}</span>
    </header>
  );
}

// 2. 列表与子组件渲染
function UserList({ users }) {
  return (
    <div className="user-container">
      <Header title="Active Users" badge={users.length} />
      <ul>
        {users.map(u => (
          <li key={u.id} className="user-item">
            <strong>{u.name}</strong> - <span>{u.role}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}

// 3. 根组件与 Fragment 嵌套
const users = [
  { id: 1, name: "Alice", role: "Frontend Architect" },
  { id: 2, name: "Bob", role: "Go Core Developer" },
  { id: 3, name: "Charlie", role: "Tailwind Designer" }
];

const App = (
  <>
    <div id="app" className="root-layout">
      <UserList users={users} />
    </div>
  </>
);

console.log("=== JSX Render Output ===");
console.log("App root type:", App.type);
console.log("App children count:", App.children.length);
console.log("Header rendered title:", App.children[0].children[0].children[0].children[0]);
console.log("User items count:", App.children[0].children[0].children[1].children.length);
console.log("✅ React JSX / TSX parsed and executed successfully on Aluka Pure Go Runtime!");
