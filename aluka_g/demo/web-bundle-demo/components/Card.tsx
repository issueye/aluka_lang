import * as React from '../react.ts';

export function Card(props) {
  return <section className="card">
    <h2 className="card-title">{props.title}</h2>
    <span className="tag">{props.tag}</span>
    <div className="card-body">{props.children}</div>
  </section>;
}
